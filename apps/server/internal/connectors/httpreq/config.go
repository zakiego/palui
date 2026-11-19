package httpreq

import (
	"errors"

	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// Config is one instance's settings.
type Config struct {
	Domains       []Domain
	Auth          string // header, query or none
	HeaderName    string
	ValueTemplate string
	QueryParam    string
	Policy        string // ask or allow; anything else reads as ask
}

// ConfigFrom applies the manifest defaults (kit.ConfigOf(Manifest, raw): the
// runtime gets the stored config, without defaults) and the cross-field rules.
func ConfigFrom(raw *jsonx.Object, secret string) (Config, error) {
	o := kit.ConfigOf(Manifest, raw)
	str := func(k, def string) string {
		v, _ := o.Get(k)
		if s, ok := v.(string); ok && strx.Trim(s) != "" {
			return strx.Trim(s)
		}
		return def
	}
	cfg := Config{
		Auth:          str("auth", "header"),
		HeaderName:    str("headerName", "Authorization"),
		ValueTemplate: str("valueTemplate", "Bearer {value}"),
		QueryParam:    str("queryParam", ""),
		Policy:        str("policy", "ask"),
	}
	if cfg.Auth != "query" && cfg.Auth != "none" {
		cfg.Auth = "header"
	}
	if cfg.Policy != "allow" {
		cfg.Policy = "ask"
	}
	domains := str("domains", "")
	if domains == "" {
		return cfg, errors.New("Set the allowed domains.")
	}
	list, err := ParseDomains(domains)
	if err != nil {
		return cfg, err
	}
	cfg.Domains = list
	if cfg.Auth == "query" && cfg.QueryParam == "" {
		return cfg, errors.New("Set the query parameter name.")
	}
	if cfg.Auth != "none" && secret == "" {
		return cfg, errors.New("This connector needs a key.")
	}
	if cfg.Auth != "none" {
		for _, d := range cfg.Domains {
			if d.Any {
				return cfg, errors.New("A key needs named domains. * only works with auth none.")
			}
		}
	}
	return cfg, nil
}
