"""The base model every record extends."""


class BaseModel:  # def BaseModel
    def __init__(self, ident):  # def BaseModel.__init__
        self.ident = ident

    def save(self):  # def BaseModel.save
        return self.validate()  # call validate -> BaseModel.validate

    def validate(self):  # def BaseModel.validate
        return True

    def delete(self):  # def BaseModel.delete
        return self.ident

    def describe(self):  # def BaseModel.describe
        return str(self.ident)
