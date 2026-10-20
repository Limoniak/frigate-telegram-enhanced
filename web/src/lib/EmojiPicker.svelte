<script>
  // A camera's emoji: picked from those a camera's place suggests, or typed for any other.
  import { T } from "./i18n.svelte.js";

  let { value = "", label, onchange } = $props();
  let open = $state(false);

  // Doors and buildings, outdoors, vehicles, animals, rooms, then the watchful ones.
  const EMOJIS = [
    "🚪", "🏠", "🏡", "🏢", "🏭", "🏚️", "🛖", "⛺", "🅿️", "🛣️", "🚧", "🧱",
    "🌳", "🌲", "🌷", "🌻", "🪴", "⛲", "🏊", "🌊", "🚗", "🚙", "🛻", "🚚",
    "🏍️", "🚲", "🛴", "🚤", "🐕", "🐈", "🐓", "🐴", "🐄", "🐑", "🐝", "🦊",
    "🛋️", "🛏️", "🍳", "🛁", "🧺", "🧰", "📦", "📬", "🔔", "🔒", "👶", "🧸",
    "📷", "🎥", "👁️", "⭐", "🌙", "☀️",
  ];

  function pick(e) {
    open = false;
    onchange(e);
  }
</script>

<div class="flex flex-col gap-2">
  <div class="flex flex-wrap items-center gap-2">
    <input class="field w-24 text-center" maxlength="16" placeholder="🚪" aria-label={label}
      {value} onchange={(e) => onchange(e.currentTarget.value.trim())}>
    <button type="button" class="btn" aria-expanded={open} onclick={() => (open = !open)}>{T("Choose an emoji")}</button>
    {#if value}<button type="button" class="btn-link" onclick={() => pick("")}>{T("Remove")}</button>{/if}
  </div>
  {#if open}
    <div role="group" aria-label={T("Choose an emoji")} class="emoji-grid grid grid-cols-8 gap-1 sm:grid-cols-12">
      {#each EMOJIS as e (e)}
        <button type="button" aria-pressed={value === e} onclick={() => pick(e)}
          class="grid h-10 place-items-center rounded-[10px] text-[20px] transition-colors hover:bg-[var(--glass-hi)] {value === e ? 'ring-2 ring-[var(--accent)]' : ''}">{e}</button>
      {/each}
    </div>
  {/if}
</div>
