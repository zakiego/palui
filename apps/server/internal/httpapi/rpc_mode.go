package httpapi

// Mode is the server's config mode ("dev" or "prod"), for the dev-only
// functions (M8b rpcdev: the fixture toggle).
func (c *Call) Mode() string { return c.S.o.Config.Mode }
