"""Accounts, and an unrelated class that reuses their method names."""
from shop.models.base import BaseModel


class Account(BaseModel):  # def Account
    def __init__(self, ident, owner):  # def Account.__init__
        super().__init__(ident)  # call __init__ -> BaseModel.__init__
        self.owner = owner

    def validate(self):  # def Account.validate
        return bool(self.owner)

    def close(self):  # def Account.close
        self.save()  # call save -> BaseModel.save
        return self.delete()  # call delete -> BaseModel.delete

    def balance(self):  # def Account.balance
        return 0


class Audit:  # def Audit
    """Not a model: the same method names as Account, on an unrelated class."""

    def save(self):  # def Audit.save
        return self.validate()  # call validate -> Audit.validate

    def validate(self):  # def Audit.validate
        return False

    def close(self):  # def Audit.close
        return None
