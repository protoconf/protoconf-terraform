package importing

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mitchellh/cli"
	assert "github.com/stretchr/testify/require"
)

func TestGenerate(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform not found in PATH, skipping")
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, "src")
	providersSource := `
	terraform {
		required_providers {
		  http = { source = "hashicorp/http" }
		  cloudflare = { source = "cloudflare/cloudflare", version = "4.13.0"}
		  aws = { source = "hashicorp/aws"}
		  google = { source = "hashicorp/google"}
		  azurerm = { source = "hashicorp/azurerm"}
		  kubernetes = { source = "hashicorp/kubernetes"}
		}
	  }
	`
	os.WriteFile(filepath.Join(dir, "providers.tf"), []byte(providersSource), os.ModePerm)
	g := NewGenerator(dir, dst, cli.NewMockUi())
	err := g.PopulateProviders()
	assert.NoError(t, err)
	for name := range g.Providers {
		log.Println("found", name)
	}
	err = g.Save()
	assert.NoError(t, err)
	Print(g.Importer.MasterFile)
	// t.Fail()
}
