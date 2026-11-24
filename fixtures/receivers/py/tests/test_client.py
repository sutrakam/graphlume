def client():  # def fixture.client
    return None


def test_index(client):
    response = client.get("/")  # call get -> none; nocall client
    return response
