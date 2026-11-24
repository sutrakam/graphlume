package shop

import (
	"errors"
	"html/template"
	"log"
	"net/http"
)

// Header is a package variable that shares its name with a method of
// http.ResponseWriter.
var Header = "X-Shop"

type node struct{}

func (n *node) addRoute(path string) {} // def node.addRoute

// Context wraps a request.
type Context struct {
	Request *http.Request
}

func (c *Context) Set(key string, value any) {} // def Context.Set

// RouterGroup is embedded in Engine, which promotes its methods.
type RouterGroup struct{}

func (g *RouterGroup) GET(path string) {} // def RouterGroup.GET

// Engine is the router.
type Engine struct {
	RouterGroup
	root *node
}

func New() *Engine { return &Engine{} } // def New

func Dir(root string) string { return root } // def Dir

func (e *Engine) pick() *node { return e.root } // def Engine.pick

func (e *Engine) addRoute(path string) { // def Engine.addRoute
	root := e.pick()    // call pick -> Engine.pick high
	root.addRoute(path) // call addRoute -> node.addRoute
}

func (e *Engine) LoadHTMLGlob(pattern string) {} // def Engine.LoadHTMLGlob

func (e *Engine) Run(c *Context, w http.ResponseWriter) error {
	e.GET("/")                     // call GET -> RouterGroup.GET high
	c.Set("k", 1)                  // call Set -> Context.Set high
	c.Request.Header.Set("k", "v") // call Set -> none
	_ = w.Header().Get(Header)     // call Header -> external
	_ = log.New(nil, "", 0)        // call New -> external
	_ = template.New("t")          // call New -> external
	_ = http.Dir(".")              // call Dir -> external
	_ = New()                      // call New -> New high
	_ = Dir(".")                   // call Dir -> Dir high
	return errors.New("stop")      // call New -> external
}
