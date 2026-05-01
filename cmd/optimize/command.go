package optimize

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mitchellh/cli"
	"github.com/protoconf/protoconf-terraform/pkg/optimize"
)

type cliCommand struct {
	ui cli.Ui
}

type cliConfig struct {
	root            string
	protoPath       string
	materializedDir string
	deleteOrphans   bool
	dryRun          bool
}

func newFlagSet() (*flag.FlagSet, *cliConfig) {
	flags := flag.NewFlagSet("", flag.ExitOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: optimize [OPTION]...")
		flags.PrintDefaults()
	}

	config := &cliConfig{}
	flags.StringVar(&config.root, "root", ".", "protoconf project root (contains src/ and materialized_config/)")
	flags.StringVar(&config.protoPath, "proto", "", "path to terraform.proto to rewrite (default <root>/src/terraform/v1/terraform.proto)")
	flags.StringVar(&config.materializedDir, "materialized", "", "materialized_config dir to scan (default <root>/materialized_config)")
	flags.BoolVar(&config.deleteOrphans, "delete-orphans", false, "delete per-family .proto files no longer referenced by terraform.proto")
	flags.BoolVar(&config.dryRun, "dry-run", false, "compute the would-be result; do not write")

	return flags, config
}

func (c *cliCommand) Run(args []string) int {
	flags, config := newFlagSet()
	flags.Parse(args)

	srcRoot := filepath.Join(config.root, "src")
	if config.protoPath == "" {
		config.protoPath = filepath.Join(srcRoot, "terraform", "v1", "terraform.proto")
	}
	if config.materializedDir == "" {
		config.materializedDir = filepath.Join(config.root, "materialized_config")
	}

	report, err := optimize.Optimize(optimize.Options{
		ProtoPath:       config.protoPath,
		MaterializedDir: config.materializedDir,
		SrcRoot:         srcRoot,
		DeleteOrphans:   config.deleteOrphans,
		DryRun:          config.dryRun,
	})
	if err != nil {
		c.ui.Error(fmt.Sprintf("optimize failed: %v", err))
		return 1
	}

	var buf bytes.Buffer
	report.Format(&buf, config.dryRun)
	c.ui.Output(buf.String())
	return 0
}

func (c *cliCommand) Help() string {
	var b bytes.Buffer
	b.WriteString(c.Synopsis())
	b.WriteString("\n")
	flags, _ := newFlagSet()
	flags.SetOutput(&b)
	flags.Usage()
	return b.String()
}

func (c *cliCommand) Synopsis() string {
	return "Prunes terraform.proto down to only the messages used by configs in materialized_config/."
}

// NewCommand is a cli.CommandFactory.
func NewCommand() (cli.Command, error) {
	ui := &cli.BasicUi{
		Reader:      os.Stdin,
		Writer:      os.Stdout,
		ErrorWriter: os.Stderr,
	}
	return &cliCommand{ui: ui}, nil
}
