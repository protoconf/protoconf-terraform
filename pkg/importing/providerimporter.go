package importing

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform/plugin/discovery"
	"github.com/jhump/protoreflect/desc/builder"
	"github.com/mitchellh/cli"
	"github.com/protoconf/protoconf-terraform/pkg/importing/parse"
	"github.com/protoconf/protoconf/importers"
)

// ProviderImporter queries a Terraform provider binary for its schema
// and returns a proto FileBuilder
type ProviderImporter struct {
	importer *importers.Importer
	meta     discovery.PluginMeta
	ui       cli.Ui
}

// NewProviderImporter returns a ProviderImporter
func NewProviderImporter(name string, schemaResponse *parse.Provider, importer *importers.Importer, ui cli.Ui) (*ProviderImporter, error) {
	meta := discovery.PluginMeta{Name: name, Version: discovery.VersionStr("1")}
	p := &ProviderImporter{importer: importer, meta: meta, ui: &cli.PrefixedUi{OutputPrefix: importer.MasterFile.Package, Ui: ui}}

	tfmsg := importer.MasterFile.GetMessage("Terraform")
	resources := tfmsg.GetNestedMessage("Resources")
	datasources := tfmsg.GetNestedMessage("Datasources")
	providers := tfmsg.GetNestedMessage("Providers")

	p.populateResources(resources, schemaResponse.ResourceSchemas)
	p.populateResources(datasources, schemaResponse.DataSourceSchemas)
	providerFile := resourceFile(importer, name, "provider", fmt.Sprintf("%d", schemaResponse.Provider.Version), name)
	providerConfigMsg := p.schemaToProtoMessage(capitalizeMessageName(name), schemaResponse.Provider)
	providerConfigMsg.AddField(builder.NewField("alias", builder.FieldTypeString()))
	providerFile.AddMessage(providerConfigMsg)
	providers.AddField(builder.NewField(name, builder.FieldTypeMessage(providerFile.GetMessage(providerConfigMsg.GetName()))).SetRepeated())

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
		family := strings.Split(n, "_")[1]
		file := resourceFile(p.importer, p.meta.Name, strings.ToLower(msg.GetName()), string(p.meta.Version), family)
		m := p.schemaToProtoMessage(capitalizeMessageName(n), s)
		file.TryAddMessage(m)
		f := builder.NewMapField(n, builder.FieldTypeString(), builder.FieldTypeMessage(file.GetMessage(m.GetName())))
		f.SetJsonName(n)
		msg.AddField(f)
	}
	return msg
}
