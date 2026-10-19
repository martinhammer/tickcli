// SPDX-License-Identifier: GPL-3.0-or-later

// Package tickbuddy is a Go client for the Tickbuddy Nextcloud app's OCS API.
//
// It does every API call, sign-in (Nextcloud Login Flow v2), credential
// storage, track name matching and date handling, so that a front end such
// as the tickcli command or the TickLLMe MCP server only adds its own layer.
//
// Nothing in this package prints. Every call takes a context.Context and
// returns typed errors: test them with errors.Is against the Err* values.
//
// A typical use:
//
//	creds := tickbuddy.NewStoredCredentials(tickbuddy.DefaultStore())
//	c := tickbuddy.New(creds, tickbuddy.WithUserAgent("MyTool/1.0"))
//	tracks, err := c.Tracks(ctx, false)
//	...
//	t, err := tickbuddy.Resolve(tracks, "exercise", false)
//	res, err := c.SetYesNo(ctx, t.ID, tickbuddy.Today(), true, nil)
package tickbuddy
