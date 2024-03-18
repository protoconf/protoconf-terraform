package importing

import (
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"

	"github.com/jhump/protoreflect/desc/builder"
	"github.com/jhump/protoreflect/desc/protoprint"
	"github.com/zclconf/go-cty/cty"

	"github.com/protoconf/protoconf-terraform/pkg/importing/meta"
	"github.com/protoconf/protoconf-terraform/pkg/importing/parse"
	"github.com/protoconf/protoconf-terraform/pkg/wktbuilders"
	"github.com/protoconf/protoconf/importers"
)

var metaFile *builder.FileBuilder = meta.MetaFile()
var breakLinesRegex *regexp.Regexp

func init() {
	breakLinesRegex = regexp.MustCompile(`(.{1,80}\S)(?:[\r\n\f\v ]+|$)`)
}

// NewFile returns a FileBuilder prepared with the required filename format
func NewFile(providerName, kind, version, family string) *builder.FileBuilder {
	v := strings.Split(version, ".")[0]
	file := builder.NewFile(fmt.Sprintf("terraform/%s/%s/v%s/%s.proto", providerName, kind, v, family))
	file.SetProto3(true)
	file.SetPackageName(fmt.Sprintf("terraform.%s.%s.v%s", providerName, kind, v))

	file.PackageComments = builder.Comments{LeadingComment: fmt.Sprintf("Provider: %s %s", providerName, version)}
	return file
}

func resourceFile(i *importers.Importer, providerName, kind, version, family string) *builder.FileBuilder {
	tmp := NewFile(providerName, kind, version, family)
	if file, ok := i.Files[tmp.GetName()]; ok {
		return file
	}
	i.RegisterFile(tmp)
	return tmp
}

// Print prints a FileBuilder to stderr
func Print(b *builder.FileBuilder) {
	p := &protoprint.Printer{}
	desc, err := b.Build()
	if err != nil {
		log.Fatal(err)
	}
	str, err := p.PrintProtoToString(desc)
	if err != nil {
		log.Fatal(err)
	}
	log.Println(str)
}

func (p *ProviderImporter) schemaToProtoMessage(name string, schema *parse.Schema) *builder.MessageBuilder {
	m := p.msgBuilderFromBlock(name, schema.Block)
	c := builder.Comments{LeadingComment: fmt.Sprintf("%s version is %d", name, schema.Version)}
	m.SetComments(c)

	// for_each
	fieldForEach := builder.NewField("for_each", builder.FieldTypeMessage(wktbuilders.StructBuilder.GetMessage("Value"))).SetJsonName("for_each")
	m.AddField(fieldForEach)
	// Adding meta fields
	metaMsg := metaFile.GetMessage("MetaFields")
	for _, field := range metaMsg.GetChildren() {
		f := metaMsg.GetField(field.GetName())
		fBuilder := builder.NewField(f.GetName(), f.GetType()).SetJsonName(f.GetName())
		if f.IsRepeated() {
			fBuilder.SetRepeated()
		}
		m.AddField(fBuilder)
	}

	fieldLifecycle := builder.NewField("lifecycle", builder.FieldTypeMessage(metaFile.GetMessage("Lifecycle")))
	m.AddField(fieldLifecycle)
	return m
}

func (p *ProviderImporter) msgBuilderFromBlock(name string, b *parse.Block) *builder.MessageBuilder {
	m := builder.NewMessage(name)
	attrs := b.Attributes
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	blocks := b.BlockTypes
	block_keys := make([]string, 0, len(blocks))
	for k := range blocks {
		block_keys = append(block_keys, k)
	}
	sort.Strings(block_keys)

	for _, fieldName := range keys {
		p.attributeToProtoField(m, fieldName, attrs[fieldName])
	}

	for _, n := range block_keys {
		nb := blocks[n]
		nm := p.msgBuilderFromBlock(capitalizeMessageName(n), nb.Block)
		f := builder.NewField(n, builder.FieldTypeMessage(nm))
		if nb.MaxItems != 1 {
			f.SetRepeated()
		}
		f.SetJsonName(n)
		if err := m.TryAddField(f); err != nil {
			p.ui.Error(fmt.Sprintf("failed to add field: %v", err))
		} else {
			m.TryAddNestedMessage(nm)
		}
	}
	return m
}

func (p *ProviderImporter) attributeToProtoField(msg *builder.MessageBuilder, name string, attr *parse.Attribute) *builder.MessageBuilder {
	t := attr.AttributeType

	err := p.handleCty(msg, name, t, attr.Description)
	if err != nil {
		log.Fatal(err)
	}
	return msg
}

func (p *ProviderImporter) handleCty(parent *builder.MessageBuilder, fieldName string, t cty.Type, description string) error {
	f := p.ctyTypeToProtoField(fieldName, t)

	if t.IsListType() || t.IsSetType() {
		t = t.ElementType()
		f.SetRepeated()
	}
	if t.IsObjectType() {
		p.handleObject(fieldName, t, f, parent)
	}

	comments := breakLinesRegex.FindAllString(description, -1)

	c := builder.Comments{
		LeadingComment: strings.Join(comments, "\n"),
	}
	f.SetComments(c)
	return parent.TryAddField(f)
}

func (p *ProviderImporter) handleObject(name string, t cty.Type, f *builder.FieldBuilder, msg *builder.MessageBuilder) {
	m := builder.NewMessage(capitalizeMessageName(name))
	keys := []string{}
	for n := range t.AttributeTypes() {
		keys = append(keys, n)
	}
	sort.Strings(keys)

	for _, n := range keys {
		t2 := t.AttributeType(n)
		f2 := p.ctyTypeToProtoField(n, t2)
		c := builder.Comments{LeadingComment: fmt.Sprintf("%v: %s", n, t2.FriendlyName())}
		if t2.IsListType() || t2.IsSetType() {
			t2 = t2.ElementType()
			f2.SetRepeated()
		}
		if t2.IsObjectType() {
			p.handleObject(n, t2, f2, m)
		}
		f2.SetComments(c)
		m.TryAddField(f2)
	}

	if err := msg.TryAddNestedMessage(m); err != nil {
		log.Fatal("failed to add message", err)
	}
	f.SetType(builder.FieldTypeMessage(m))
}

func (p *ProviderImporter) ctyTypeToProtoField(name string, t cty.Type) *builder.FieldBuilder {
	jsonName := name
	var validFieldName = regexp.MustCompile(`^[a-z]`)
	if !validFieldName.MatchString(name) {
		name = "_" + name
	}
	f := builder.NewField(name, builder.FieldTypeString())
	f.SetJsonName(jsonName)

	if t.IsMapType() {
		f = builder.NewMapField(name, builder.FieldTypeString(), builder.FieldTypeString()).SetJsonName(name)
		return f
	}
	if t.IsListType() || t.IsSetType() || t.IsCollectionType() {
		t = t.ElementType()
		f.SetRepeated()
	}
	if t.IsObjectType() {
		return f
	}
	f.SetType(ctyTypeToProtoFieldType(t))
	return f
}

func ctyTypeToProtoFieldType(t cty.Type) *builder.FieldType {
	var ft *builder.FieldType
	if t.IsCollectionType() {
		t = t.ElementType()
	}
	switch x := t.FriendlyName(); x {
	case "string":
		return builder.FieldTypeString()
	case "number":
		return builder.FieldTypeInt64()
	case "bool":
		return builder.FieldTypeBool()
	case "dynamic":
		return builder.FieldTypeMessage(wktbuilders.StructBuilder.GetMessage("Value"))
	case "object":
		return builder.FieldTypeMessage(wktbuilders.StructBuilder.GetMessage("Value"))
	default:
		log.Fatalf("unknown type: %v", x)
	}
	return ft
}

func capitalizeMessageName(s string) string {
	out := []string{}
	a := strings.Split(s, "_")
	for _, item := range a {
		out = append(out, strings.Title(item))
	}
	return strings.Join(out, "")
}
