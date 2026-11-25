package com.pay.service;

public class TransactionServiceTest {

    @Test
    public void updatesStatusAndNotifies() {
        service.updateStatus("txn-1", "COMPLETED");
        verify(repository).save(any());
        verify(notificationService).send("txn-1", "COMPLETED");
    }

    @Test
    public void findsTransactionById() {
        assertNotNull(service.findTransaction("txn-1"));
    }

    // Declared after its uses on purpose: the lines above are pinned by tests
    // that name them. Without a declaration `service` is a name nothing
    // states a type for, and its calls bind by name alone.
    private TransactionService service;
}
