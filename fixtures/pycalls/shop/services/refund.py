"""A subclass: inherited dispatch, super(), and an override."""
from shop.models import Account
from shop.services.payment import PaymentService


class RefundService(PaymentService):  # def RefundService
    def charge(self, account: Account, amount):  # def RefundService.charge
        self.record(-amount)  # call record -> PaymentService.record
        return super().charge(account, -amount)  # call charge -> PaymentService.charge

    def refund(self, account: Account, amount):  # def RefundService.refund
        self.charge(account, amount)  # call charge -> RefundService.charge
        self.settle(account)  # call settle -> PaymentService.settle
        return account.balance()  # call balance -> Account.balance
