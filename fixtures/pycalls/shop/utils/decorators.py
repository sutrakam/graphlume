"""Decorators: a decorated definition is still the definition."""
import functools


def traced(fn):  # def decorators.traced
    @functools.wraps(fn)
    def wrapper(*args, **kwargs):  # def decorators.traced.wrapper
        return fn(*args, **kwargs)  # call fn -> ?

    return wrapper


def retry(times):  # def decorators.retry
    def decorate(fn):  # def decorators.retry.decorate
        def wrapper(*args, **kwargs):  # def decorators.retry.wrapper
            result = None
            for _ in range(times):
                result = fn(*args, **kwargs)  # call fn -> ?
            return result

        return wrapper

    return decorate
