"""Dynamic dispatch the resolver must not guess at."""
from shop.models import Account, Audit, Ledger


def pick(kind):  # def dynamic.pick
    if kind == "audit":
        return Audit()  # call Audit -> Audit
    return Account(1, "a")  # call Account -> Account


def run(kind):  # def dynamic.run
    record = pick(kind)  # call pick -> dynamic.pick
    record.save()  # call save -> ?
    return record.close()  # call close -> ?


def reassigned(flag):  # def dynamic.reassigned
    thing = Account(1, "a")  # call Account -> Account
    if flag:
        thing = Audit()  # call Audit -> Audit
    return thing.validate()  # call validate -> ?


def by_name(obj, name):  # def dynamic.by_name
    method = getattr(obj, name)
    return method()  # call method -> ?


def by_table(kind):  # def dynamic.by_table
    table = {"ledger": Ledger, "audit": Audit}
    model = table[kind]
    return model().save()  # call save -> ?


def duck(items):  # def dynamic.duck
    for item in items:
        item.close()  # call close -> ?
