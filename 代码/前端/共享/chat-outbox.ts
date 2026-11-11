export type PendingMessage = { id: string; body: string; client: string };

/** Keep uncertain sends separate: visiting another conversation must not replace a retry key. */
export class ChatOutbox {
  private entries = new Map<string, PendingMessage>();

  prepare(id: string, body: string, createID: () => string): PendingMessage {
    const previous = this.entries.get(id);
    if (previous?.body === body) return previous;
    const message = { id, body, client: createID() };
    this.entries.set(id, message);
    return message;
  }

  acknowledge(message: PendingMessage) {
    // A late acknowledgement cannot erase a replacement draft or a new login's send.
    if (this.entries.get(message.id) === message) this.entries.delete(message.id);
  }

  clear() {
    this.entries.clear();
  }
}
