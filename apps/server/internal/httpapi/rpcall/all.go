// Package rpcall links every dashboard RPC package into the binary. Each
// package registers its handlers with httpapi.Register in init().
package rpcall

import (
	_ "github.com/zakiego/palui/apps/server/internal/httpapi/rpcdev"
	_ "github.com/zakiego/palui/apps/server/internal/httpapi/rpcpeople"
	_ "github.com/zakiego/palui/apps/server/internal/httpapi/rpcsched"
	_ "github.com/zakiego/palui/apps/server/internal/httpapi/rpcsetup"
)
