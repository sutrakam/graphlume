import debounce from "lodash/debounce";
import { BillingClient } from "../clients/payments";
import type { Payment } from "../types";

const client = new BillingClient();

// Capturing twice in quick succession must reuse one key.
export const capturePayment = debounce(
  async (id: string): Promise<Payment> => client.Capture(id, `capture-${id}`),
  300,
  { leading: true, trailing: false },
);

export function renderStatus(p: Payment): string {
  return `${p.id}: ${p.status} (${p.amount.cents / 100} ${p.amount.currency})`;
}
