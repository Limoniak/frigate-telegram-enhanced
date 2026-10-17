<script>
  // A Telegram phone (dark theme) showing a notification: model says which media and
  // blocks it has (preview.js), entry what it is about. image is a thumbnail URL, or
  // empty for the sample photo, with a box on the person.
  import sample from "../assets/sample.jpg";
  import { T, Tf } from "./i18n.svelte.js";
  import { labelText } from "./labels.js";
  let { model, entry, image = "" } = $props();
  const media = $derived(model.inMessage ?? model.reply);
</script>

<div class="w-full max-w-[290px] rounded-[36px] bg-[#0b0d10] p-[9px] shadow-[0_40px_90px_-30px_rgba(0,0,0,.9)] ring-1 ring-white/10">
  <div class="overflow-hidden rounded-[28px] bg-[#0e1621] text-[#e9edf1]">
    <div class="flex items-center gap-2.5 bg-[#17212b] px-3 py-2.5">
      <span class="h-8 w-8 rounded-full bg-gradient-to-br from-[#37aee2] to-[#1e96c8]"></span>
      <div class="leading-tight"><div class="text-[13px] font-semibold">Frigate</div><div class="text-[11px] text-[#7d8b99]">bot</div></div>
    </div>
    <div class="grid gap-1.5 p-2.5 text-[12px] leading-[1.45]">
      <div class="overflow-hidden rounded-[14px] rounded-bl-[4px] bg-[#182533]">
        {#if model.photo}
          <div class="relative aspect-[16/10] overflow-hidden bg-[#26313b]">
            {#if image}<img class="absolute inset-0 h-full w-full object-cover" src={image} alt="">
            {:else}
              <div class="absolute inset-0 origin-[50%_61%] transition-transform duration-700 {model.crop ? 'scale-[1.55]' : ''}">
                <img class="absolute inset-0 h-full w-full object-cover" src={sample} alt="">
                <div class="absolute left-[43%] top-[33%] h-[58%] w-[14%] rounded-[3px] border-2 border-amber-300"></div>
              </div>
            {/if}
            {#if model.inMessage}<span class="absolute bottom-2 left-2 rounded-full bg-black/55 px-2 py-0.5 text-[10.5px]">{model.inMessage === "gif" ? "GIF" : "▶ 0:14"}</span>{/if}
          </div>
        {/if}
        <div class="px-2.5 pb-2 pt-2">
          <b>{labelText(entry.label, entry.sound, T)}</b> — {entry.camera}
          {#if entry.details}<br>{entry.details}{/if}
          <br>🕑 {entry.time}
          <br><span class="text-[#6ab2f2]">{T("🔗 Open in Frigate")}</span>
          {#if model.genai}<div class="mt-1.5 italic text-[#aab8c4]">{T("A person walks up to the front door.")}</div>{/if}
          {#if model.grouped && entry.grouped?.length}
            <div class="mt-1.5 border-t border-white/10 pt-1.5 text-[#aab8c4]">➕ {Tf("%d more detection(s):", entry.grouped.length)}
              {#each entry.grouped as g (g.time + g.label)}<br>• {g.time} {labelText(g.label, false, T)} — {g.camera}{/each}
            </div>
          {/if}
        </div>
      </div>
      <div class="grid grid-cols-2 gap-1 text-center text-[11px]">
        <span class="rounded-[10px] bg-white/10 py-1.5">{T("📷 Now")}</span><span class="rounded-[10px] bg-white/10 py-1.5">🎬 Clip</span>
        <span class="rounded-[10px] bg-white/10 py-1.5">🔇 1 h</span><span class="rounded-[10px] bg-white/10 py-1.5">⏸️ 30 min</span>
        {#if model.homeAssistant}<span class="col-span-2 rounded-[10px] bg-white/10 py-1.5">🏠 Home Assistant</span>{/if}
      </div>
      {#if model.reply}
        <div class="mt-1 overflow-hidden rounded-[14px] rounded-bl-[4px] bg-[#182533] p-2">
          <div class="mb-1 border-l-2 border-[#6ab2f2] pl-2 text-[11px] text-[#aab8c4]">{T("↩️ As a reply")}</div>
          <div class="relative aspect-[16/10] overflow-hidden rounded-[10px] bg-[#26313b]">
            <img class="absolute inset-0 h-full w-full object-cover" src={image || sample} alt="">
            <span class="absolute bottom-2 left-2 rounded-full bg-black/55 px-2 py-0.5 text-[10.5px]">{media === "gif" ? "GIF" : "▶ 0:14"}</span>
          </div>
        </div>
      {/if}
    </div>
  </div>
</div>
