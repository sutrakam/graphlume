import importlib.metadata


def write(data):  # def py.write
    return data


def package_version():
    return importlib.metadata.version("mixed")  # call version -> none
