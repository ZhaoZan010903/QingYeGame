export type PendingMessage = {
  id: string;
  body: string;
  client: string;
  images: string[];
};

/** Keep uncertain sends separate: visiting another conversation must not replace a retry key. */
export class ChatOutbox {
  private entries = new Map<string, PendingMessage>();

  prepare(
    id: string,
    body: string,
    createID: () => string,
    images: string[] = [],
  ): PendingMessage {
    const previous = this.entries.get(id);
    if (
      previous?.body === body &&
      JSON.stringify(previous.images) === JSON.stringify(images)
    )
      return previous;
    const message = { id, body, client: createID(), images: [...images] };
    this.entries.set(id, message);
    return message;
  }

  acknowledge(message: PendingMessage) {
    // A late acknowledgement cannot erase a replacement draft or a new login's send.
    if (this.entries.get(message.id) === message)
      this.entries.delete(message.id);
  }

  clear() {
    this.entries.clear();
  }
}
