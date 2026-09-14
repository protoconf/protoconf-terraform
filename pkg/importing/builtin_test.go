package importing

import (
	"path/filepath"
	"testing"

	"github.com/jhump/protoreflect/desc/protoparse"
	"github.com/mitchellh/cli"
	assert "github.com/stretchr/testify/require"
)

func TestBuiltinRemoteState(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "src")
	g := NewGenerator(t.TempDir(), dst, cli.NewMockUi())
	g.addBuiltinDatasources()
	assert.NoError(t, g.Save())

	fds, err := protoparse.Parser{ImportPaths: []string{dst}}.ParseFiles("terraform/v1/terraform.proto")
	assert.NoError(t, err)
	field := fds[0].FindMessage("terraform.v1.Terraform.Datasources").FindFieldByName("terraform_remote_state")
	assert.NotNil(t, field)
	msg := field.GetMapValueType().GetMessageType()
	assert.Equal(t, "terraform.builtin.datasources.v1.TerraformRemoteState", msg.GetFullyQualifiedName())
	for _, name := range []string{"backend", "config", "defaults", "outputs", "workspace"} {
		assert.NotNil(t, msg.FindFieldByName(name), name)
	}
}
