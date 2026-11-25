"""A test module: stubs that reuse production method names."""
from shop import Account
from shop.services import PaymentService
from shop.services.refund import RefundService


class FakeLedger:  # def FakeLedger
    def post(self):  # def FakeLedger.post
        return None

    def save(self):  # def FakeLedger.save
        return None


def make_service():  # def test_payment.make_service
    return PaymentService(FakeLedger())  # call PaymentService -> PaymentService


def test_charge():  # def test_payment.test_charge
    service = make_service()  # call make_service -> test_payment.make_service
    account = Account(1, "owner")  # call Account -> Account
    account.save()  # call save -> BaseModel.save
    account.close()  # call close -> Account.close
    service.charge(account, 10)  # call charge -> PaymentService.charge


def test_refund():  # def test_payment.test_refund
    refunds = RefundService(FakeLedger())  # call RefundService -> RefundService
    refunds.refund(Account(2, "owner"), 5)  # call refund -> RefundService.refund
    refunds.settle(Account(3, "owner"))  # call settle -> PaymentService.settle
    ledger = FakeLedger()  # call FakeLedger -> FakeLedger
    ledger.save()  # call save -> FakeLedger.save
    ledger.post()  # call post -> FakeLedger.post
