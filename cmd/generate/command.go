package generate

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/mitchellh/cli"
	"github.com/protoconf/protoconf-terraform/pkg/importing"
)

type cliCommand struct {
	ui cli.Ui
}

type cliConfig struct {
	importPath string
	outputPath string
}

func newFlagSet() (*flag.FlagSet, *cliConfig) {
	flags := flag.NewFlagSet("", flag.ExitOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: [OPTION]...")
		flags.PrintDefaults()
	}

	config := &cliConfig{}
	flags.StringVar(&config.importPath, "import_path", ".", "Path of terraform workspace")
	flags.StringVar(&config.outputPath, "output", "src", "Path to write proto files to.")

	return flags, config
}

func (c *cliCommand) Run(args []string) int {
	flags, config := newFlagSet()
	flags.Parse(args)

	g := importing.NewGenerator(config.importPath, config.outputPath, c.ui)
	err := g.PopulateProviders()
	if err != nil {
		c.ui.Error(fmt.Sprintf("Failed to generate providers: %v", err))
		return 1
	}
	err = g.Save()
	if err != nil {
		c.ui.Error(fmt.Sprintf("failed to write proto files: %v", err))
		return 1
	}
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
	return "Creates proto files from terraform providers schema"
}

// NewCommand is a cli.CommandFactory
func NewCommand() (cli.Command, error) {
	ui := &cli.BasicUi{
		Reader:      os.Stdin,
		Writer:      os.Stdout,
		ErrorWriter: os.Stderr,
	}
	return &cliCommand{ui: ui}, nil
}
