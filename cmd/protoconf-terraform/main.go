package main

import (
	"fmt"
	"os"

	"github.com/mitchellh/cli"
	"github.com/protoconf/protoconf-terraform/cmd/generate"
	initcmd "github.com/protoconf/protoconf-terraform/cmd/init"
	"github.com/protoconf/protoconf-terraform/cmd/run"
	"github.com/protoconf/protoconf-terraform/pkg/build"
)

func main() {
	ui := cli.BasicUi{
		Reader:      os.Stdin,
		Writer:      os.Stdout,
		ErrorWriter: os.Stderr,
	}
	cmd := cli.NewCLI("protoconf-terraform", build.Version)
	cmd.Args = os.Args[1:]
	cmd.Commands = map[string]cli.CommandFactory{
		"init":     initcmd.NewCommand,
		"run":      run.NewCommand,
		"generate": generate.NewCommand,
	}

	code, err := cmd.Run()
	if err != nil {
		ui.Error(fmt.Sprintf("error: %v", err))

	}
	os.Exit(code)

}
