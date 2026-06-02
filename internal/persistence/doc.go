// Package persistence owns local SQLite workspaces for simulator state.
//
// Workspace databases are opened in WAL mode with a busy timeout. Write
// transactions should stay short: compute inputs before opening the transaction,
// write the minimum required rows, and commit promptly. Long report reads should
// run outside write transactions so WAL readers do not delay local writes.
package persistence
