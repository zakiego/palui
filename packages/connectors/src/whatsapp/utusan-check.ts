// The utusan binding check lives in core (packages/core/src/utusan.ts), where the
// inbox uses it on every new WhatsApp chat. Re-exported for the WhatsApp wizard.
export { checkUtusan, utusanBindsChat, UTUSAN_CONFIG_PATH, type UtusanBinding } from "@palui/core/utusan"
