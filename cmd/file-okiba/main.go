package main

import (
	"context"
	"fmt"
	"github.com/olduvai-jp/file-okiba-cli/internal/cli"
	"golang.org/x/term"
	"os"
	"os/signal"
	"strings"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cli.Version = version
	interactive := term.IsTerminal(int(os.Stdin.Fd()))
	var terminalState *term.State
	if interactive {
		terminalState, _ = term.GetState(int(os.Stdin.Fd()))
	}
	restore := func() {
		if terminalState != nil {
			_ = term.Restore(int(os.Stdin.Fd()), terminalState)
		}
	}
	streams := cli.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Interactive: interactive}
	if interactive {
		streams.ReadSecret = func(label string) (string, error) {
			fmt.Fprintf(os.Stderr, "%s: ", label)
			b, e := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			return string(b), e
		}
	}
	root := cli.New(streams)
	if err := root.ExecuteContext(ctx); err != nil {
		restore()
		jsonMode, _ := root.PersistentFlags().GetBool("json")
		for _, arg := range os.Args[1:] {
			if arg == "--json" || strings.HasPrefix(arg, "--json=true") {
				jsonMode = true
			}
		}
		cli.ReportError(os.Stderr, err, jsonMode)
		os.Exit(cli.ExitCode(err))
	}
	restore()
}
