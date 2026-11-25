// Package web wraps a process-wide engine, the way gin's ginS does.
package web

import "example.com/shop"

func current() *shop.Engine { return shop.New() } // call New -> New high

// LoadHTMLGlob has the name of the method it forwards to.
func LoadHTMLGlob(pattern string) { // def web.LoadHTMLGlob
	current().LoadHTMLGlob(pattern) // call LoadHTMLGlob -> Engine.LoadHTMLGlob
}
