// Package optimize prunes the master terraform.proto down to only the
// messages actually populated by Terraform configs found under a
// materialized_config directory. See cmd/optimize for the CLI shell.
package optimize

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/builder"
	"github.com/jhump/protoreflect/desc/protoparse"
	"github.com/jhump/protoreflect/desc/protoprint"
)

// loadPathRE matches `load("//<path>", ...)` statements in Starlark
// sources. The leading `//` rooting is the protoconf convention for
// "path relative to src/". Captured group 1 is the path.
var loadPathRE = regexp.MustCompile(`load\s*\(\s*["']//([^"']+)["']`)

// terraformTypeURL is the protobuf Any type_url that identifies a
// materialized output as a terraform.v1.Terraform message. Materialized
// JSON files whose top-level value carries this @type are scanned for
// resource/datasource/provider field usage.
const terraformTypeURL = "type.googleapis.com/terraform.v1.Terraform"

// Options configures a single Optimize run.
type Options struct {
	// ProtoPath is the master terraform.proto to rewrite in place.
	ProtoPath string
	// MaterializedDir is the materialized_config root scanned for usage.
	MaterializedDir string
	// SrcRoot is the directory used as the proto import root (typically
	// <project>/src). Used by the parser and the orphan sweep.
	SrcRoot string
	// DeleteOrphans removes per-family .proto files no longer referenced
	// by the slimmed terraform.proto. Skipped when DryRun is true (the
	// would-be deletions are still listed in Report.WouldDeleteFiles).
	DeleteOrphans bool
	// DryRun computes the would-be result without touching disk.
	DryRun bool
}

// Report describes what Optimize did (or would do, in dry-run).
type Report struct {
	UsedResources      []string
	UsedDatasources    []string
	UsedProviders      []string
	RemovedResources   []string
	RemovedDatasources []string
	RemovedProviders   []string
	// WouldDeleteFiles lists per-family .proto files that the orphan
	// sweep removed (or would remove, in dry-run). Paths are relative
	// to SrcRoot.
	WouldDeleteFiles []string
}

// Optimize rewrites opts.ProtoPath to keep only the resource / datasource /
// provider fields populated by any terraform.v1.Terraform message under
// opts.MaterializedDir. Returns a Report regardless of whether DryRun is
// set; in dry-run mode no files are touched.
func Optimize(opts Options) (*Report, error) {
	used, err := scanMaterialized(opts.MaterializedDir)
	if err != nil {
		return nil, fmt.Errorf("scan materialized: %w", err)
	}
	if len(used.resources)+len(used.datasources)+len(used.providers) == 0 {
		return nil, fmt.Errorf("no terraform.v1.Terraform messages found under %q; aborting (pruning to empty would be catastrophic)", opts.MaterializedDir)
	}

	report := &Report{
		UsedResources:   sortedKeys(used.resources),
		UsedDatasources: sortedKeys(used.datasources),
		UsedProviders:   sortedKeys(used.providers),
	}

	relProto, err := filepath.Rel(opts.SrcRoot, opts.ProtoPath)
	if err != nil {
		return nil, fmt.Errorf("ProtoPath %q is not under SrcRoot %q: %w", opts.ProtoPath, opts.SrcRoot, err)
	}

	parser := &protoparse.Parser{ImportPaths: []string{opts.SrcRoot, ""}}
	descs, err := parser.ParseFiles(relProto)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", opts.ProtoPath, err)
	}
	if len(descs) != 1 {
		return nil, fmt.Errorf("expected 1 file descriptor for %s, got %d", relProto, len(descs))
	}
	fileBuilder, err := builder.FromFile(descs[0])
	if err != nil {
		return nil, fmt.Errorf("FromFile: %w", err)
	}

	tfMsg := fileBuilder.GetMessage("Terraform")
	if tfMsg == nil {
		return nil, fmt.Errorf("master proto %s missing top-level Terraform message", opts.ProtoPath)
	}

	report.RemovedResources = pruneNested(tfMsg, "Resources", used.resources)
	report.RemovedDatasources = pruneNested(tfMsg, "Datasources", used.datasources)
	report.RemovedProviders = pruneNested(tfMsg, "Providers", used.providers)

	// Drop imports no longer reachable from any surviving field. Without
	// this, FromFile preserves every original import, even ones whose
	// types are no longer referenced.
	fileBuilder.PruneUnusedDependencies()

	fd, err := fileBuilder.Build()
	if err != nil {
		return report, fmt.Errorf("rebuild file descriptor: %w", err)
	}

	if opts.DeleteOrphans {
		kept := keptImports(fd)
		// Also keep anything explicitly load()'d from any .mpconf/.pinc/
		// .pconf source. The user may import a type without instantiating
		// it (e.g. for a downstream linker), in which case it never reaches
		// materialized output but still needs to exist on disk.
		loaded, err := scanLoadStatements(opts.SrcRoot)
		if err != nil {
			return report, fmt.Errorf("scan load statements: %w", err)
		}
		for path := range loaded {
			kept[path] = true
		}
		report.WouldDeleteFiles, err = planOrphanDeletions(opts.SrcRoot, kept)
		if err != nil {
			return report, fmt.Errorf("plan orphan deletions: %w", err)
		}
	}

	if opts.DryRun {
		return report, nil
	}

	if err := writeProto(opts.ProtoPath, fd); err != nil {
		return report, err
	}

	if opts.DeleteOrphans {
		for _, rel := range report.WouldDeleteFiles {
			if err := os.Remove(filepath.Join(opts.SrcRoot, rel)); err != nil {
				return report, fmt.Errorf("delete orphan %s: %w", rel, err)
			}
		}
		// Best-effort: prune now-empty parent directories.
		pruneEmptyDirs(filepath.Join(opts.SrcRoot, "terraform"))
	}

	return report, nil
}

// pruneNested removes fields from tfMsg's named nested message whose name
// is not in `used`. Returns the sorted list of removed field names.
//
// For map fields the auto-generated *Entry nested message is also removed
// — TryRemoveField only severs the field's symbol, leaving the entry as
// an orphaned nested message that keeps the per-family import alive. We
// don't rely on FieldBuilder.IsMap() because after builder.FromFile() the
// field-to-entry backlink (flb.msgType) is not populated and IsMap()
// always returns false. Instead we just derive the expected entry name
// from the field name and unconditionally try to remove it; if no such
// entry exists (non-map field), TryRemoveNestedMessage returns false
// harmlessly.
func pruneNested(tfMsg *builder.MessageBuilder, nested string, used map[string]bool) []string {
	m := tfMsg.GetNestedMessage(nested)
	if m == nil {
		return nil
	}
	// Snapshot field names; mutating during GetChildren iteration is unsafe.
	var fieldNames []string
	for _, child := range m.GetChildren() {
		if fb, ok := child.(*builder.FieldBuilder); ok {
			fieldNames = append(fieldNames, fb.GetName())
		}
	}
	var removed []string
	for _, name := range fieldNames {
		if used[name] {
			continue
		}
		if !m.TryRemoveField(name) {
			continue
		}
		removed = append(removed, name)
		m.TryRemoveNestedMessage(mapEntryTypeName(name))
	}
	sort.Strings(removed)
	return removed
}

// mapEntryTypeName mirrors jhump/protoreflect's internal entryTypeName:
// snake_case field name -> PascalCase + "Entry". E.g. aws_instance ->
// AwsInstanceEntry. We can't import the internal helper, so reproduce it.
func mapEntryTypeName(fieldName string) string {
	var out []rune
	upper := true
	for _, r := range fieldName {
		if r == '_' {
			upper = true
			continue
		}
		if upper {
			out = append(out, []rune(strings.ToUpper(string(r)))...)
			upper = false
		} else {
			out = append(out, r)
		}
	}
	return string(out) + "Entry"
}

// usedSet collects the field names populated under value.resource,
// value.data, and value.provider across every scanned Terraform output.
type usedSet struct {
	resources, datasources, providers map[string]bool
}

func newUsedSet() *usedSet {
	return &usedSet{
		resources:   make(map[string]bool),
		datasources: make(map[string]bool),
		providers:   make(map[string]bool),
	}
}

// matFile / matValue mirror the materialized JSON shape we care about.
// The full document is `{ "protoFile": ..., "value": <inner> }` where
// the inner carries `@type` and the populated message fields.
type matFile struct {
	Value json.RawMessage `json:"value"`
}
type matValue struct {
	AtType   string                     `json:"@type"`
	Resource map[string]json.RawMessage `json:"resource"`
	Data     map[string]json.RawMessage `json:"data"`
	Provider map[string]json.RawMessage `json:"provider"`
}

func scanMaterialized(root string) (*usedSet, error) {
	used := newUsedSet()
	if root == "" {
		return used, nil
	}
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".materialized_JSON") {
			return nil
		}
		return absorbMaterialized(path, used)
	})
	return used, walkErr
}

func absorbMaterialized(path string, used *usedSet) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var outer matFile
	if err := json.Unmarshal(b, &outer); err != nil {
		// Not every materialized file has the {protoFile, value} shape; skip silently.
		return nil
	}
	if len(outer.Value) == 0 {
		return nil
	}
	var v matValue
	if err := json.Unmarshal(outer.Value, &v); err != nil {
		return nil
	}
	if v.AtType != terraformTypeURL {
		return nil
	}
	for k := range v.Resource {
		used.resources[k] = true
	}
	for k := range v.Data {
		used.datasources[k] = true
	}
	for k := range v.Provider {
		used.providers[k] = true
	}
	return nil
}

// keptImports returns the set of proto file paths the slimmed file still
// imports. Derived from the rebuilt descriptor (after PruneUnusedDependencies),
// so it reflects exactly what will be written to disk.
func keptImports(fd *desc.FileDescriptor) map[string]bool {
	kept := make(map[string]bool, len(fd.GetDependencies()))
	for _, dep := range fd.GetDependencies() {
		kept[dep.GetName()] = true
	}
	return kept
}

// scanLoadStatements walks srcRoot for .mpconf, .pconf, and .pinc files
// and returns the set of paths referenced via `load("//<path>", ...)`.
// Paths are relative to srcRoot (the same shape as keptImports). The
// orphan sweep treats these as kept so loaded-but-not-instantiated types
// (e.g. dead imports the user hasn't cleaned up yet) don't get deleted
// out from under their .mpconf.
func scanLoadStatements(srcRoot string) (map[string]bool, error) {
	loaded := map[string]bool{}
	err := filepath.Walk(srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		switch filepath.Ext(path) {
		case ".mpconf", ".pconf", ".pinc":
			// scan
		default:
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range loadPathRE.FindAllSubmatch(b, -1) {
			loaded[string(m[1])] = true
		}
		return nil
	})
	return loaded, err
}

// planOrphanDeletions enumerates .proto files under SrcRoot/terraform/
// whose path is not in `kept`. terraform/v1/* (master + meta + util.pinc)
// is always preserved. Returned paths are relative to srcRoot, sorted.
func planOrphanDeletions(srcRoot string, kept map[string]bool) ([]string, error) {
	base := filepath.Join(srcRoot, "terraform")
	var deletions []string
	err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".proto") {
			return nil
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		// Always keep the v1 scaffolding (terraform.proto + meta.proto).
		if strings.HasPrefix(rel, filepath.Join("terraform", "v1")+string(filepath.Separator)) {
			return nil
		}
		if kept[rel] {
			return nil
		}
		deletions = append(deletions, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(deletions)
	return deletions, nil
}

// pruneEmptyDirs removes empty subdirectories under root. Best-effort —
// errors are ignored because leaving empty dirs behind is not catastrophic.
func pruneEmptyDirs(root string) {
	// Two passes (depth-first delete, then re-check) handles the case where
	// removing leaves makes parents empty.
	for i := 0; i < 8; i++ {
		removed := 0
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || !info.IsDir() || path == root {
				return nil
			}
			entries, err := os.ReadDir(path)
			if err == nil && len(entries) == 0 {
				if os.Remove(path) == nil {
					removed++
				}
			}
			return nil
		})
		if removed == 0 {
			return
		}
	}
}

func writeProto(path string, fd *desc.FileDescriptor) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()
	printer := &protoprint.Printer{}
	if err := printer.PrintProtoFile(fd, f); err != nil {
		return fmt.Errorf("print %s: %w", path, err)
	}
	return nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Format prints a human-readable summary of a Report to w.
func (r *Report) Format(w io.Writer, dryRun bool) {
	verb := "removed"
	if dryRun {
		verb = "would remove"
	}
	fmt.Fprintf(w, "used: %d resources, %d datasources, %d providers\n",
		len(r.UsedResources), len(r.UsedDatasources), len(r.UsedProviders))
	fmt.Fprintf(w, "%s %d resource fields, %d datasource fields, %d provider fields from terraform.proto\n",
		verb, len(r.RemovedResources), len(r.RemovedDatasources), len(r.RemovedProviders))
	if len(r.WouldDeleteFiles) > 0 {
		dverb := "deleting"
		if dryRun {
			dverb = "would delete"
		}
		fmt.Fprintf(w, "%s %d orphan .proto files\n", dverb, len(r.WouldDeleteFiles))
	}
}
