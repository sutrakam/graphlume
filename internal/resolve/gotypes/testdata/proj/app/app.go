package app

import (
	"fmt"
	"strconv"

	"example.com/proj/shapes"
	"example.com/proj/util"
)

func Run() {
	c := shapes.Circle{R: 2}
	s := &shapes.Square{S: 3}
	fmt.Println(c.Area(), s.Area())
	fmt.Println(util.Make().Name())
	area := c.Area
	_ = area
	_ = shapes.Wrapper{}.Hello()
	var sh shapes.Shape = c
	_ = shapes.Describe(sh)
	_ = util.Map[int, string]([]int{1}, strconv.Itoa)
	var st util.Stack[int]
	st.Push(1)
	func() { _ = util.Make().Inc() }()
	_ = shapes.Circle(shapes.Circle{R: 1})
	_ = handlerName()
}

func handlerName() string { return platformName() }
