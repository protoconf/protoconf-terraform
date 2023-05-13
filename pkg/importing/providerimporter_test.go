package importing

import (
	"log"
	"testing"

	"github.com/mitchellh/cli"
	assert "github.com/stretchr/testify/require"
)

func TestBuilder(t *testing.T) {
	log.Println("starting")
	provider := "google"
	version := "4.64.0"
	// meta, err := findPlugin("provider", "random", "2.2.1")
	meta, err := findPlugin("provider", provider, version)
	assert.NoError(t, err)
	config := newGRPCClientConfig(meta)
	client, err := NewGRPCClient(config)
	assert.NoError(t, err)
	defer client.Close()
	g := NewGenerator("", "", cli.NewMockUi())
	p, err := NewProviderImporter(*meta, client, g.Importer, cli.NewMockUi())
	assert.NoError(t, err)
	Print(p.importer.MasterFile)
	// Print(p.Resources)
	// Print(p.Datasources)
	// Print(p.Provider)
	// t.Fail()
}
