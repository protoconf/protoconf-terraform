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
	name := parts[len(parts)-1]
	meta := discovery.PluginMeta{Name: name, Version: discovery.VersionStr(strings.Split(schemaResponse.ProviderVersion, ".")[0])}
	p := &ProviderImporter{importer: importer, meta: meta, ui: &cli.PrefixedUi{OutputPrefix: importer.MasterFile.Package, Ui: ui}}

	tfmsg := importer.MasterFile.GetMessage("Terraform")
	resources := tfmsg.GetNestedMessage("Resources")
	datasources := tfmsg.GetNestedMessage("Datasources")
	providers := tfmsg.GetNestedMessage("Providers")

	p.populateResources(resources, schemaResponse.ResourceSchemas)
	p.populateResources(datasources, schemaResponse.DataSourceSchemas)
	providerFile := resourceFile(importer, name, "provider", string(meta.Version), name)
	providerFile.IsProto3 = false
	providerConfigMsg := p.schemaToProtoMessage(capitalizeMessageName(name), schemaResponse.Provider)
	providerConfigMsg.AddField(builder.NewField("alias", builder.FieldTypeString()))
	providerConfigMsg.AddField(builder.NewField("provider_fqdn", builder.FieldTypeString()).SetDefaultValue(fqdn))
	providerConfigMsg.AddField(builder.NewField("provider_version", builder.FieldTypeString()).SetDefaultValue(string(schemaResponse.ProviderVersion)))

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
