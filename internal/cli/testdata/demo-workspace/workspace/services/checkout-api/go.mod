module example.com/payments-platform/services/checkout-api

go 1.22

require (
	example.com/payments-platform/libs/payments-core v0.0.0
	example.com/payments-platform/services/ledger v0.0.0
	golang.org/x/net v0.17.0
)

require golang.org/x/text v0.13.0 // indirect

replace (
	example.com/payments-platform/libs/payments-core => ../../libs/payments-core
	example.com/payments-platform/services/ledger => ../ledger
)
