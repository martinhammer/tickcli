// SPDX-License-Identifier: GPL-3.0-or-later

// Command tickcli is the command-line client for Tickbuddy.
package main

import (
	"os"

	"github.com/martinhammer/tickcli/internal/cli"
)

func main() {
	os.Exit(cli.Main())
}
