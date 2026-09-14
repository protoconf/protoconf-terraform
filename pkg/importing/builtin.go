package importing

import (
	"github.com/hashicorp/terraform/plugin/discovery"
	"github.com/zclconf/go-cty/cty"

	"github.com/protoconf/protoconf-terraform/pkg/importing/parse"
)

const builtinProviderPrefix = "terraform.io/builtin/"

// builtinDatasources mirrors terraform.io/builtin/terraform, which
// `terraform providers schema -json` only reports when the config already
// uses it (and without a version), so it is hand-declared here instead.
var builtinDatasources = map[string]*parse.Schema{
	"terraform_remote_state": {Block: &parse.Block{Attributes: map[string]*parse.Attribute{
		"backend": {
			AttributeType: cty.String,
			Required:      true,
			Description:   "The remote backend to use, e.g. `remote` or `http`.",
		},
		"config": {
			AttributeType: cty.DynamicPseudoType,
			Optional:      true,
			Description:   "The configuration of the remote backend. Accepts any arguments valid in the equivalent `terraform { backend \"<TYPE>\" { ... } }` block.",
		},
		"defaults": {
			AttributeType: cty.DynamicPseudoType,
			Optional:      true,
			Description:   "Default values for outputs, in case the state file is empty or lacks a required output.",
		},
		"outputs": {
			AttributeType: cty.DynamicPseudoType,
			Computed:      true,
			Description:   "An object containing every root-level output in the remote state.",
		},
		"workspace": {
			AttributeType: cty.String,
			Optional:      true,
			Description:   "The Terraform workspace to use, if the backend supports workspaces.",
		},
	}}},
}

// addBuiltinDatasources registers builtinDatasources on Terraform.Datasources.
// The package is `terraform.builtin.*`, not `terraform.terraform.*`, to avoid
// the same proto name-resolution collision described in safeProviderPackageName.
func (g *Generator) addBuiltinDatasources() {
	p := &ProviderImporter{
		importer: g.Importer,
		meta:     discovery.PluginMeta{Name: "builtin", Version: "1"},
		ui:       g.ui,
	}
	datasources := g.Importer.MasterFile.GetMessage("Terraform").GetNestedMessage("Datasources")
	p.populateResources(datasources, builtinDatasources)
}
