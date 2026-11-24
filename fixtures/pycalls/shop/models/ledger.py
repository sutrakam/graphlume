"""A second model that overrides what Account inherits."""
from shop.models.base import BaseModel


class Ledger(BaseModel):  # def Ledger
    def save(self):  # def Ledger.save
        self.post()  # call post -> Ledger.post
        return super().save()  # call save -> BaseModel.save

    def post(self):  # def Ledger.post
        return self.describe()  # call describe -> BaseModel.describe

    def close(self):  # def Ledger.close
        return self.save()  # call save -> Ledger.save
