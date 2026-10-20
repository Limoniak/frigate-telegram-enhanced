<script>
  // The dialog of ask() (confirm.svelte.js): modal, so that the focus stays in it, on
  // Cancel first; Escape cancels.
  import { T } from "./i18n.svelte.js";
  import { pending } from "./confirm.svelte.js";

  let dialog;
  $effect(() => { if (pending.resolve && !dialog.open) dialog.showModal(); });

  function answer(yes) {
    const resolve = pending.resolve;
    pending.resolve = null;
    if (dialog.open) dialog.close();
    resolve?.(yes);
  }
</script>

<dialog bind:this={dialog} id="confirm" class="confirm" aria-labelledby="confirm-question" onclose={() => answer(false)}>
  <p id="confirm-question" class="m-0">{pending.question}</p>
  <div class="mt-5 flex flex-wrap justify-end gap-2">
    <button type="button" class="btn" id="confirm-cancel" onclick={() => answer(false)}>{T("Cancel")}</button>
    <button type="button" class="btn btn-primary" id="confirm-ok" onclick={() => answer(true)}>{pending.ok}</button>
  </div>
</dialog>
