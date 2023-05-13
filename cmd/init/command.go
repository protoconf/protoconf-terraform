package initcmd

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchellh/cli"
	"github.com/protoconf/libprotoconf"
	protoconf_terraform "github.com/protoconf/protoconf-terraform"
	"github.com/protoconf/protoconf-terraform/proto/protoconf_terraform/config/v1"
)

type Command struct {
	flagset *flag.FlagSet
	config  *config.TerraformInitCommand
	ui      cli.Ui
}

var _ cli.Command = &Command{}

func NewCommand() (cli.Command, error) {
	ui := &cli.BasicUi{
		Reader:      os.Stdin,
		Writer:      os.Stdout,
		ErrorWriter: os.Stderr,
	}
	colorUi := &cli.ColoredUi{
		Ui: ui,
	}
	config := &config.TerraformInitCommand{
		OutputPath: "tmp",
	}
	lpc := libprotoconf.NewConfig(config)
	flagset := lpc.DefaultFlagSet()
	return &Command{
		config:  config,
		flagset: flagset,
		ui:      colorUi,
	}, nil
}

func (c *Command) Synopsis() string {
	return "Initializes the protoconf-terraform workspace."
}

func (c *Command) Help() string {
	buf := &strings.Builder{}
	c.flagset.SetOutput(buf)
	c.flagset.Usage()
	return fmt.Sprintf("%s\n\n%v", c.Synopsis(), buf.String())
}

func (c *Command) Run(args []string) int {
	c.flagset.Parse(args)
	template := protoconf_terraform.InitTemplate
	err := fs.WalkDir(template, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		fullpath := filepath.Join(c.config.OutputPath, path)

		if d.IsDir() {
			return os.MkdirAll(fullpath, 0755)
		}
		b, err := template.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(fullpath, b, 0644)
	})
	if err != nil {
		c.ui.Error(fmt.Sprintf("init error: %v", err))
		return 1
	}
	return 0
}
