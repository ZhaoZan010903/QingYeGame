import type { Contact } from '../types';

// Channel names, not display names, decide which app can scan the QR code.
export function contactsForChannel(contacts: Contact[], channel?: 'qq') {
  return channel ? contacts.filter((c) => c.channel.trim().toLowerCase() === channel) : contacts;
}
