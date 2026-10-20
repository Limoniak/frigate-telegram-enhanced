<script>
  // A Telegram phone showing a notification, in Telegram's night mode or its day mode as
  // the page's theme (the --tg-* tokens of app.css): model says which media and
  // blocks it has (preview.js), entry what it is about. image is a thumbnail URL (no
  // box, no zoom), or empty for the sample photo, with a box on the person. A real
  // notification (real) shows no made-up description or clip length.
  import sample from "../assets/sample.jpg";
  import { T, Tf } from "./i18n.svelte.js";
  import { labelText } from "./labels.js";
  import Logo from "./Logo.svelte";
  let { model, entry, image = "", real = false } = $props();
  const badge = (m) => (m === "gif" ? "GIF" : real ? "▶" : "▶ 0:14");
  const media = $derived(model.inMessage ?? model.reply);
</script>

<!-- The phone itself follows the theme too: black on the night, white on the light page. -->
<div class="w-full max-w-[290px] rounded-[36px] bg-[var(--phone-frame)] p-[9px] shadow-[var(--phone-shadow)] ring-1 ring-[var(--phone-ring)]">
  <div class="overflow-hidden rounded-[28px] bg-[var(--tg-chat)] text-[var(--tg-ink)] ring-1 ring-[var(--phone-ring)]">
    <div class="flex items-center gap-2.5 bg-[var(--tg-header)] px-3 py-2.5">
      <!-- The bot's picture (docs/images/bot-avatar.png): the mark on the night, in its dark colors
           in both modes: it is a picture, the same in Telegram's day and night. -->
      <span class="grid h-8 w-8 flex-none place-items-center rounded-full bg-[#0b1220] [--logo-dot:#7cc8f5] [--logo-ring:#2aabee]" aria-hidden="true"><Logo class="h-5 w-5" /></span>
      <div class="leading-tight"><div class="text-[13px] font-semibold">Frigate</div><div class="text-[11px] text-[var(--tg-sub)]">bot</div></div>
    </div>
    <div class="grid gap-1.5 p-2.5 text-[12px] leading-[1.45]">
      <div class="overflow-hidden rounded-[14px] rounded-bl-[4px] bg-[var(--tg-bubble)]">
        {#if model.photo}
          <div class="relative aspect-[16/10] overflow-hidden bg-[var(--tg-media)]">
            {#if image}<img class="absolute inset-0 h-full w-full object-cover" src={image} alt="">
            {:else}
              <div class="absolute inset-0 origin-[50%_80%] transition-transform duration-700 {model.crop ? 'scale-[1.55]' : ''}">
                <img class="absolute inset-0 h-full w-full object-cover" src={sample} alt="">
                <div class="absolute left-[43%] top-[33%] h-[58%] w-[14%] rounded-[3px] border-2 border-amber-300"></div>
              </div>
            {/if}
            {#if model.inMessage}<span class="absolute bottom-2 left-2 rounded-full bg-black/55 px-2 py-0.5 text-[10.5px] text-white">{badge(model.inMessage)}</span>{/if}
          </div>
        {/if}
        <div class="px-2.5 pb-2 pt-2">
          <b>{labelText(entry.label, entry.sound, T)}</b> — {entry.camera}
          {#if entry.sub_label}<br>🏷 {entry.sub_label}{/if}
          {#if entry.details}<br>{entry.details}{/if}
          <br>🕑 {entry.time}
          <br><span class="text-[var(--tg-link)]">{T("🔗 Open in Frigate")}</span>
          {#if model.genai && !real}<div class="mt-1.5 italic text-[var(--tg-muted)]">{T("A person walks up to the front door.")}</div>{/if}
          {#if model.grouped && entry.grouped?.length}
            <div class="mt-1.5 border-t border-[var(--tg-line)] pt-1.5 text-[var(--tg-muted)]">➕ {Tf("%d more detection(s):", entry.grouped.length)}
              {#each entry.grouped as g (g.time + g.label)}<br>• {g.time} {labelText(g.label, false, T)} — {g.camera}{/each}
            </div>
          {/if}
        </div>
      </div>
      <div class="grid grid-cols-2 gap-1 text-center text-[11px]">
        <span class="rounded-[10px] bg-[var(--tg-button)] py-1.5">{T("📷 Now")}</span><span class="rounded-[10px] bg-[var(--tg-button)] py-1.5">{T("🎬 Clip")}</span>
        <span class="rounded-[10px] bg-[var(--tg-button)] py-1.5">🔇 1 h</span><span class="rounded-[10px] bg-[var(--tg-button)] py-1.5">⏸️ 30 min</span>
        {#if model.homeAssistant}<span class="col-span-2 rounded-[10px] bg-[var(--tg-button)] py-1.5">🏠 Home Assistant</span>{/if}
      </div>
      {#if model.reply}
        <div class="mt-1 overflow-hidden rounded-[14px] rounded-bl-[4px] bg-[var(--tg-bubble)] p-2">
          <div class="mb-1 border-l-2 border-[var(--tg-link)] pl-2 text-[11px] text-[var(--tg-muted)]">{T("↩️ As a reply")}</div>
          <div class="relative aspect-[16/10] overflow-hidden rounded-[6px] bg-[var(--tg-media)]">
            <img class="absolute inset-0 h-full w-full object-cover" src={image || sample} alt="">
            <span class="absolute bottom-2 left-2 rounded-full bg-black/55 px-2 py-0.5 text-[10.5px] text-white">{badge(media)}</span>
          </div>
        </div>
      {/if}
    </div>
  </div>
</div>
