"""Module-level functions whose names collide with methods elsewhere."""


def render(value):  # def fmt.render
    return str(value)


def close(handle):  # def fmt.close
    return handle


def save(path, data):  # def fmt.save
    return (path, data)


def money(amount):  # def fmt.money
    return render(amount)  # call render -> fmt.render
