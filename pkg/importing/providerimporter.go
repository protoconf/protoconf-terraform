package importing

import (
	"sort"
	"strings"

	"github.com/hashicorp/terraform/plugin/discovery"
	"github.com/jhump/protoreflect/desc/builder"
	"github.com/mitchellh/cli"
	"github.com/protoconf/protoconf-terraform/pkg/importing/parse"
	"github.com/protoconf/protoconf/importers"
)

// safeProviderPackageName remaps Terraform provider names that would collide
// with well-known proto package prefixes when used as a proto package segment.
// Currently only `google` collides (with `google.protobuf.*`): a package like
// `terraform.google.resources.v6` causes proto resolvers to find the local
// `google` segment and fail to fall back to the global `google.protobuf` WKTs.
var safeProviderPackageName = map[string]string{
	"google": "googlecloud",
}

func safeProviderName(name string) string {
	if mapped, ok := safeProviderPackageName[name]; ok {
		return mapped
	}
	return name
}

// ProviderImporter queries a Terraform provider binary for its schema
// and returns a proto FileBuilder
type ProviderImporter struct {
	importer *importers.Importer
	meta     discovery.PluginMeta
	ui       cli.Ui
}

// NewProviderImporter returns a ProviderImporter
func NewProviderImporter(fqdn string, schemaResponse *parse.Provider, importer *importers.Importer, ui cli.Ui) (*ProviderImporter, error) {
	parts := strings.Split(fqdn, "/")
	providerName := parts[len(parts)-1]
	providerSafeName := safeProviderName(providerName)
	meta := discovery.PluginMeta{Name: providerSafeName, Version: discovery.VersionStr(strings.Split(schemaResponse.ProviderVersion, ".")[0])}
	p := &ProviderImporter{importer: importer, meta: meta, ui: &cli.PrefixedUi{OutputPrefix: importer.MasterFile.Package, Ui: ui}}

	tfmsg := importer.MasterFile.GetMessage("Terraform")
	resources := tfmsg.GetNestedMessage("Resources")
	datasources := tfmsg.GetNestedMessage("Datasources")
	providers := tfmsg.GetNestedMessage("Providers")

	p.populateResources(resources, schemaResponse.ResourceSchemas)
	p.populateResources(datasources, schemaResponse.DataSourceSchemas)
	providerFile := resourceFile(importer, providerSafeName, "provider", string(meta.Version), providerSafeName)
	providerFile.IsProto3 = false
	providerConfigMsg := p.schemaToProtoMessage(capitalizeMessageName(providerName), schemaResponse.Provider)
	providerConfigMsg.AddField(builder.NewField("alias", builder.FieldTypeString()))
	providerConfigMsg.AddField(builder.NewField("provider_fqdn", builder.FieldTypeString()).SetDefaultValue(fqdn))
	providerConfigMsg.AddField(builder.NewField("provider_version", builder.FieldTypeString()).SetDefaultValue(string(schemaResponse.ProviderVersion)))

	providerFile.AddMessage(providerConfigMsg)
	providers.AddField(builder.NewField(providerName, builder.FieldTypeMessage(providerFile.GetMessage(providerConfigMsg.GetName()))).SetRepeated())

	return p, nil
}

func (p *ProviderImporter) populateResources(msg *builder.MessageBuilder, schema map[string]*parse.Schema) *builder.MessageBuilder {
	keys := []string{}
	for n := range schema {
		keys = append(keys, n)
	}
	sort.Strings(keys)

	for _, n := range keys {
		s := schema[n]
		family := n
		if strings.Contains(n, "_") {
			family = strings.Split(n, "_")[1]
		}
		file := resourceFile(p.importer, p.meta.Name, strings.ToLower(msg.GetName()), string(p.meta.Version), family)
		m := p.schemaToProtoMessage(capitalizeMessageName(n), s)
		file.TryAddMessage(m)
		f := builder.NewMapField(n, builder.FieldTypeString(), builder.FieldTypeMessage(file.GetMessage(m.GetName())))
		f.SetJsonName(n)
		msg.AddField(f)
	}
	return msg
}
