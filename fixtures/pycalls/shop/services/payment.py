"""The service most call shapes go through."""
from shop.models import Account, Ledger
from shop.utils import fmt
from shop.utils.decorators import retry, traced
from shop.utils.fmt import money


class PaymentService:  # def PaymentService
    def __init__(self, ledger: Ledger, gateway=None):  # def PaymentService.__init__
        self.ledger = ledger
        self.gateway = gateway

    @traced
    def charge(self, account: Account, amount):  # def PaymentService.charge
        account.validate()  # call validate -> Account.validate
        account.save()  # call save -> BaseModel.save
        self.record(amount)  # call record -> PaymentService.record
        return money(amount)  # call money -> fmt.money

    @retry(3)  # call retry -> decorators.retry
    def record(self, amount):  # def PaymentService.record
        entry = Ledger(amount)  # call Ledger -> Ledger
        entry.post()  # call post -> Ledger.post
        entry.save()  # call save -> Ledger.save
        return fmt.render(amount)  # call render -> fmt.render

    def settle(self, account: Account):  # def PaymentService.settle
        account.close()  # call close -> Account.close
        fmt.close(account)  # call close -> fmt.close
        return self.gateway.close()  # call close -> ?

    @classmethod
    def default(cls):  # def PaymentService.default
        return cls.build(None)  # call build -> PaymentService.build

    @staticmethod
    def build(ledger):  # def PaymentService.build
        return PaymentService(ledger)  # call PaymentService -> PaymentService

    def audit(self, target):  # def PaymentService.audit
        target.save()  # call save -> ?
        return target.validate()  # call validate -> ?
