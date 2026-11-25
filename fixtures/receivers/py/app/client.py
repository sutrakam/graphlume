class FlaskClient:
    def open(self, path):  # def FlaskClient.open
        return path

    def get(self, path):  # def FlaskClient.get
        return self.open(path)  # call open -> FlaskClient.open high


def read(path):
    with open(path) as handle:  # call open -> none
        return handle
