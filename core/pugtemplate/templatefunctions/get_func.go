package templatefunctions

import (
	"context"

	"flamingo.me/flamingo/core/pugtemplate/pugjs"
	"flamingo.me/flamingo/framework/router"
)

type (
	// GetFunc allows templates to access the router's `get` method
	GetFunc struct {
		Router *router.Router `inject:""`
	}
)

// Func as implementation of get method
func (g *GetFunc) Func(ctx context.Context) interface{} {
	return func(what string, params ...*pugjs.Map) interface{} {
		var p = make(map[interface{}]interface{})
		if len(params) == 1 {
			for k, v := range params[0].Items {
				p[k.String()] = v.String()
			}
		}
		return g.Router.Data(ctx, what, p)
	}
}
