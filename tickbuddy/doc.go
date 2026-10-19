// SPDX-License-Identifier: GPL-3.0-or-later

// Package tickbuddy is a Go client for the Tickbuddy Nextcloud app's OCS API.
//
// It does every API call, sign-in (Nextcloud Login Flow v2), credential
// storage, track name matching and date handling, so that a front end such
// as the tickcli command or the TickLLMe MCP server only adds its own layer.
//
// It is also the shared sign-in for every Tickbuddy Go client. Store finds,
// writes and deletes the stored login (environment, then keychain, then
// encrypted file); StartLogin runs Login Flow v2; Store.SignIn checks and
// stores a login; Store.Logout forgets it, revoking an app password the
// login link created; ReadStatus says who is signed in, from where, and
// what the server lacks. Clients that use these find each other's login,
// so a front end adds only its prompts, wording and exit codes. Package
// tickbuddytest is a fake server to test them against.
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
