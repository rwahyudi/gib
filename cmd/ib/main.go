package main

import (
	"fmt"
	"os"

	"github.com/rwahyudi/gib/internal/ibcli"
)

func main() {
	app, err := ibcli.NewDefaultApp()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := app.Execute(os.Args[1:]); err != nil {
		app.PrintError(err)
		os.Exit(1)
	}
}
