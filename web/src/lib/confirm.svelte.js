// A question to confirm before something that can't be undone, shown by Confirm.svelte
// in the page's look (not the browser's confirm()): ask resolves to the answer.
export const pending = $state({ question: "", ok: "", resolve: null });

export function ask(question, ok) {
  pending.resolve?.(false); // a question still open is dropped
  return new Promise((resolve) => { Object.assign(pending, { question, ok, resolve }); });
}
