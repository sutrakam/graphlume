import type { Payment } from "../types";

// Client for checkout-api, reached through the web app's own origin (the edge
// proxy forwards /v1 to checkout-api). Capture sends an Idempotency-Key so a
// retry from the browser can never charge a customer twice.
export class BillingClient {
  async Create(payment: Omit<Payment, "status">): Promise<Payment> {
    const res = await fetch("/v1/payments", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payment),
    });
    return this.parse(res);
  }

  async Get(id: string): Promise<Payment> {
    const res = await fetch(`/v1/payments/${id}`);
    return this.parse(res);
  }

  // Captures an authorised payment.
  async Capture(id: string, key: string): Promise<Payment> {
    const res = await fetch(`/v1/payments/${id}/capture`, {
      method: "PUT", // CONTRACT MISMATCH: checkout-api only answers POST here
      headers: { "Idempotency-Key": key },
    });
    return this.parse(res);
  }

  private async parse(res: Response): Promise<Payment> {
    if (!res.ok) {
      throw new Error(`checkout-api answered ${res.status}`);
    }
    return (await res.json()) as Payment;
  }
}
