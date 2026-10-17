<script>
  // A duration like 90s or 1h30m. While the text is not one, the field is flagged and
  // nothing is sent; a valid one is sent when the field is left.
  import { validDuration } from "./fields.js";

  let { value, onchange, disabled = false, label = undefined } = $props();
  let invalid = $state(false);

  function commit(e) {
    const v = e.currentTarget.value.trim();
    invalid = !validDuration(v);
    if (!invalid) onchange(v);
  }
</script>

<input type="text" class="field w-32 max-w-full" placeholder="60s" aria-label={label} aria-invalid={invalid ? "true" : undefined}
  value={value || "0s"} {disabled} oninput={() => (invalid = false)} onchange={commit} />
