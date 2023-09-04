package parse

import (
	"encoding/json"
	"os/exec"
	"regexp"

	"github.com/zclconf/go-cty/cty"
)

type Providers struct {
	FormatVersion string               `json:"format_version"`
	Schemas       map[string]*Provider `json:"provider_schemas,omitempty"`
}

type Provider struct {
	Provider          *Schema            `json:"provider,omitempty"`
	ResourceSchemas   map[string]*Schema `json:"resource_schemas,omitempty"`
	DataSourceSchemas map[string]*Schema `json:"data_source_schemas,omitempty"`
	ProviderVersion   string
}
type Schema struct {
	Version uint64 `json:"version"`
	Block   *Block `json:"block,omitempty"`
}

type Block struct {
	Attributes      map[string]*Attribute `json:"attributes,omitempty"`
	BlockTypes      map[string]*BlockType `json:"block_types,omitempty"`
	Description     string                `json:"description,omitempty"`
	DescriptionKind string                `json:"description_kind,omitempty"`
	Deprecated      bool                  `json:"deprecated,omitempty"`
}

type BlockType struct {
	NestingMode string `json:"nesting_mode,omitempty"`
	Block       *Block `json:"block,omitempty"`
	MinItems    uint64 `json:"min_items,omitempty"`
	MaxItems    uint64 `json:"max_items,omitempty"`
}

type Attribute struct {
	AttributeType       cty.Type    `json:"type,omitempty"`
	AttributeNestedType *NestedType `json:"nested_type,omitempty"`
	Description         string      `json:"description,omitempty"`
	DescriptionKind     string      `json:"description_kind,omitempty"`
	Deprecated          bool        `json:"deprecated,omitempty"`
	Required            bool        `json:"required,omitempty"`
	Optional            bool        `json:"optional,omitempty"`
	Computed            bool        `json:"computed,omitempty"`
	Sensitive           bool        `json:"sensitive,omitempty"`
}

type NestedType struct {
	Attributes  map[string]*Attribute `json:"attributes,omitempty"`
	NestingMode string                `json:"nesting_mode,omitempty"`
}

func ParseTerraformSchema(path string) (*Providers, error) {
	err := exec.Command("terraform", "-chdir="+path, "init").Run()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("terraform", "-chdir="+path, "providers", "schema", "-json")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	err = cmd.Start()
	if err != nil {
		return nil, err
	}

	schema := &Providers{}
	err = json.NewDecoder(stdout).Decode(schema)
	if err != nil {
		return nil, err
	}
	err = cmd.Wait()

	if err != nil {
		return nil, err
	}

	cmdVersion, err := exec.Command("terraform", "-chdir="+path, "version").Output()
	if err != nil {
		return nil, err
	}

	matcher := regexp.MustCompile(`provider (?P<name>.+) v(?P<version>.+)`)
	parsed := matcher.FindAllStringSubmatch(string(cmdVersion), -1)
	for _, item := range parsed {
		fqdn := item[matcher.SubexpIndex("name")]
		version := item[matcher.SubexpIndex("version")]
		schema.Schemas[fqdn].ProviderVersion = version
	}

	return schema, nil
}
