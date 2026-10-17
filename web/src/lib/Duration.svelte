<script>
  // A duration like 90s or 1h30m. While the text is not one, the field is flagged and
  // nothing is sent; a valid one is sent when the field is left.
  import { T } from "./i18n.svelte.js";
  import { validDuration } from "./fields.js";

  let { value, onchange, disabled = false, label = undefined, describedby = undefined } = $props();
  const uid = $props.id();
  let invalid = $state(false);
  let el;
  // A value or a disabled state that changes drops what was typed (and its flag).
  $effect.pre(() => { value; disabled; invalid = false; if (el) el.value = value || "0s"; });

  function commit(e) {
    const v = e.currentTarget.value.trim();
    invalid = !validDuration(v);
    if (!invalid) onchange(v);
  }
</script>

<div class="flex flex-col gap-1">
  <input bind:this={el} type="text" class="field w-32 max-w-full" placeholder="60s" aria-label={label} aria-invalid={invalid ? "true" : undefined}
    aria-describedby={[describedby, invalid && uid + "-error"].filter(Boolean).join(" ") || undefined} value={value || "0s"} {disabled} oninput={() => (invalid = false)} onchange={commit} />
  {#if invalid}<p id="{uid}-error" class="m-0 text-[12px] text-[var(--err)]" role="alert">{T("Expected a duration like 60s, 5m or 1h30m")}</p>{/if}
</div>
