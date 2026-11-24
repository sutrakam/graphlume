export interface Money {
  cents: number;
  currency: string;
}

export type PaymentStatus = "authorised" | "capture_pending" | "captured" | "refunded" | "failed";

export interface Payment {
  id: string;
  amount: Money;
  status: PaymentStatus;
  reference?: string;
}
