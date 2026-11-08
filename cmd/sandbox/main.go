// Command sandbox opens an isolated dev shell whose traffic all goes through
// the router's SOCKS proxy. See README.md.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/itkitteh/sandbox/internal/cli"
)

func main() {
	err := cli.New().Execute()
	var exit cli.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		os.Exit(exit.Code)
	default:
		fmt.Fprintln(os.Stderr, "sandbox:", err)
		os.Exit(1)
	}
}
