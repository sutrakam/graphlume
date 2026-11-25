"""Names an import brought in and a local definition then shadowed."""
from shop.utils.fmt import money, render


def render(value):  # def shadow.render
    return "<" + str(value) + ">"


def show(value):  # def shadow.show
    return render(value)  # call render -> shadow.render


def price(value):  # def shadow.price
    return money(value)  # call money -> fmt.money


def local_shadow(value):  # def shadow.local_shadow
    money = len
    return money(value)  # call money -> ?


def param_shadow(render, value):  # def shadow.param_shadow
    return render(value)  # call render -> ?
