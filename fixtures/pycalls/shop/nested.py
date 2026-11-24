"""Nested definitions that reuse one name in different scopes."""


def outer_a():  # def nested.outer_a
    def helper():  # def nested.outer_a.helper
        return 1

    return helper()  # call helper -> nested.outer_a.helper


def outer_b():  # def nested.outer_b
    def helper():  # def nested.outer_b.helper
        return 2

    return helper()  # call helper -> nested.outer_b.helper


def build_a():  # def nested.build_a
    class Item:  # def nested.build_a.Item
        def render(self):  # def nested.build_a.Item.render
            return self.label()  # call label -> nested.build_a.Item.label

        def label(self):  # def nested.build_a.Item.label
            return "a"

    return Item()  # call Item -> nested.build_a.Item


def build_b():  # def nested.build_b
    class Item:  # def nested.build_b.Item
        def render(self):  # def nested.build_b.Item.render
            return self.label()  # call label -> nested.build_b.Item.label

        def label(self):  # def nested.build_b.Item.label
            return "b"

    item = Item()  # call Item -> nested.build_b.Item
    return item.render()  # call render -> nested.build_b.Item.render


class Tree:  # def Tree
    class Node:  # def Tree.Node
        def walk(self):  # def Tree.Node.walk
            return self.visit()  # call visit -> Tree.Node.visit

        def visit(self):  # def Tree.Node.visit
            return None

    def visit(self):  # def Tree.visit
        return Tree.Node().walk()  # call walk -> Tree.Node.walk

    def walk(self):  # def Tree.walk
        return self.visit()  # call visit -> Tree.visit
