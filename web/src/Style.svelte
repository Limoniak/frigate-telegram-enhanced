<script>
  // The notification style: pick a model and its options, and see the notification
  // change on the phone next to them, before sending a sample.
  import { T, Tf, i18n } from "./lib/i18n.svelte.js";
  import { app, change, sendTest } from "./lib/store.svelte.js";
  import { STYLES, cameraOverrides, effective, seconds, styleOf } from "./lib/settings.js";
  import { previewModel } from "./lib/preview.js";
  import { routeHash } from "./lib/route.js";
  import photo from "./assets/sample.jpg";
  import Card from "./lib/Card.svelte";
  import Phone from "./lib/Phone.svelte";
  import Switch from "./lib/Switch.svelte";

  const eff = $derived(effective(app.overlay));
  const current = $derived(styleOf(eff));
  const inPlace = $derived(eff.media_in_place !== false);
  const model = $derived(previewModel(eff, { homeAssistant: !!app.overlay.home_assistant_url }));
  const overrides = $derived(cameraOverrides(app.overlay, ["snapshot", "clip", "gif", "crop", "media_in_place", "genai_description"]));
  const cams = $derived((app.data.cameras || []).map((c) => c.name).filter((n) => effective(app.overlay, n).enabled !== false));
  const sample = $derived({
    label: "person", camera: cams[0] || T("front door"), time: "21:47:05", details: T("📍 driveway · 87%"),
    grouped: [{ time: "21:48", label: "car", camera: cams[0] || T("front door") }, { time: "21:49", label: "dog", camera: cams[0] || T("front door") }],
  });

  const labels = () => ({
    clip: [T("Photo + video"), inPlace ? T("The image right away, then the video takes its place in the same message.") : T("The image right away, then the video clip as a reply when the event ends.")],
    photo: [T("Photo only"), T("Light. The clip stays one tap away with the 🎬 button.")],
    gif: [T("Photo + GIF"), inPlace ? T("The image, then a short animated preview in its place.") : T("A short animated preview instead of the video.")],
    text: [T("Text only"), T("The most discreet: just the message and the buttons.")],
  });
  const groups = () => [["0s", T("One message per detection")], ["2m", T("📦 Group over 2 min")], ["5m", T("📦 Group over 5 min")]];

  let pick = $state("");
  // The camera of the sample: the one picked while it is still enabled, else the first.
  const chosen = $derived(cams.includes(pick) ? pick : cams[0]);
  // The camera's own image; when it does not load (or there is no camera), the sample.
  const liveUrl = $derived(chosen ? "api/cameras/" + encodeURIComponent(chosen) + "/latest.jpg" : "");
  let bad = $state(""); // the address that failed to load
  let loaded = $state(""); // the address that loaded
  const live = $derived(liveUrl && bad !== liveUrl ? liveUrl : "");
  let sending = $state(false);
  let result = $state({ state: "", text: "" });

  async function test() {
    sending = true;
    result = { state: "", text: T("Sending…") };
    try {
      await sendTest(chosen);
      result = { state: "ok", text: T("Sent ✓ — check Telegram") };
    } catch (e) {
      result = { state: "err", text: T("Failed: ") + e.message };
    } finally {
      setTimeout(() => { sending = false; }, 3000);
    }
  }
</script>

<section id="style" class="grid grid-cols-1 gap-6 lg:grid-cols-12">
  <div class="flex flex-col gap-4 lg:col-span-7">
    <div class="flex flex-col gap-1"><span class="lbl">{T("Style")}</span><h1 class="m-0 text-[26px] font-semibold tracking-tight">{T("What should your notifications look like?")}</h1></div>
    <Card>
      <div id="styles" class="grid grid-cols-2 gap-2.5 sm:grid-cols-4">
        {#each STYLES as s (s.id)}
          <button type="button" data-style={s.id} aria-pressed={current === s.id} onclick={() => change(s.patch)}
            class="grid gap-2 rounded-[16px] p-2 text-left ring-1 transition-shadow duration-500 {current === s.id ? 'ring-2 ring-[var(--accent)] text-[var(--ink)]' : 'ring-[var(--ring)] text-[var(--muted)]'}">
            {#if s.id === "text"}
              <span class="grid h-10 content-center gap-1 rounded-[10px] bg-[var(--glass)] px-2"><i class="h-[3px] w-4/5 rounded bg-[var(--faint)]"></i><i class="h-[3px] w-3/5 rounded bg-[var(--faint)]"></i><i class="h-[3px] w-2/5 rounded bg-[var(--faint)]"></i></span>
            {:else}
              <span class="relative h-10 overflow-hidden rounded-[10px] bg-[#26313b]">
                <img class="h-full w-full object-cover object-[50%_45%]" src={photo} alt="">
                {#if s.id !== "photo"}<span class="absolute bottom-0.5 left-1 rounded-full bg-black/55 px-1.5 text-[9px] leading-[14px] text-white">{s.id === "gif" ? "GIF" : "▶"}</span>{/if}
              </span>
            {/if}
            <span class="font-medium">{labels()[s.id][0]}</span>
          </button>
        {/each}
      </div>
      {#if current !== "custom"}<p class="m-0 text-[var(--muted)]">{labels()[current][1]}</p>
      {:else}<p class="m-0 text-[var(--muted)]">{T("Current setting is custom (see Advanced settings).")}</p>{/if}
    </Card>
    <Card>
      <div class="flex items-center gap-4"><div class="min-w-0 flex-1"><div>{T("Crop the image on the object")}</div><div class="text-[12px] text-[var(--faint)]">{T("Much more readable on a phone. Applies to event snapshots.")}</div></div>
        <Switch id="style-crop" checked={!!eff.crop} label={T("Crop the image on the object")} onchange={(v) => change({ crop: v })} /></div>
      <div class="flex items-center gap-4"><div class="min-w-0 flex-1"><div>{T("Video in the same message")}</div><div class="text-[12px] text-[var(--faint)]">{T("The clip (or GIF) replaces the image in the notification, instead of arriving as a new message.")}</div></div>
        <Switch id="style-in-place" checked={inPlace} label={T("Video in the same message")} onchange={(v) => change({ media_in_place: v })} /></div>
      <div class="flex items-center gap-4"><div class="min-w-0 flex-1"><div>{T("Add the GenAI description")}</div><div class="text-[12px] text-[var(--faint)]">{T("When Frigate publishes it, it is added to the notification's caption.")}</div></div>
        <Switch id="style-genai" checked={!!eff.genai_description} label={T("Add the GenAI description")} onchange={(v) => change({ genai_description: v })} /></div>
      <div class="flex flex-wrap items-center gap-4"><div class="min-w-[150px] flex-1"><div>{T("Bursts")}</div><div class="text-[12px] text-[var(--faint)]">{T("Detections close in time are added to the first message (edited, no new sound) instead of sending new ones.")}</div></div>
        <div id="style-group" class="inline-flex rounded-full bg-[var(--field)] p-[3px] ring-1 ring-[var(--ring)]">
          {#each groups() as [value, label] (value)}
            <button type="button" data-group={value} aria-pressed={seconds(eff.group) === seconds(value)} onclick={() => change({ group: value })}
              class="rounded-full px-3 py-1 text-[12px] {seconds(eff.group) === seconds(value) ? 'bg-[var(--glass-hi)] text-[var(--ink)]' : 'text-[var(--muted)]'}">{label}</button>
          {/each}
        </div></div>
    </Card>
    {#if overrides.length}<p id="style-overrides" class="m-0 text-[var(--muted)]">{T("Applies to every camera, except those set differently:")} {#each overrides as name, i (name)}{i ? ", " : ""}<a href={routeHash({ screen: "cameras", camera: name })}>{name}</a>{/each}</p>{/if}
    {#if app.data.can_test && cams.length}
      <div id="try" class="flex flex-wrap items-center gap-3">
        <button type="button" class="btn btn-primary" disabled={sending} onclick={test}>{T("📨 Send me a sample")}</button>
        {#if cams.length > 1}<span class="text-[var(--muted)]">{T("from camera")}</span>
          <select class="field w-auto" aria-label={T("Camera for the sample")} value={chosen} onchange={(e) => (pick = e.currentTarget.value)}>{#each cams as c (c)}<option value={c}>{c}</option>{/each}</select>{/if}
        <span id="try-result" class="result {result.state}" role="status">{result.text}</span>
      </div>
    {/if}
  </div>
  <div id="live-preview" class="flex flex-col items-center gap-3 lg:sticky lg:top-24 lg:col-span-5 lg:self-start">
    <span class="lbl">{T("Live preview")}</span>
    <Phone {model} entry={sample} image={live} onerror={() => { bad = liveUrl; }} onload={() => { loaded = liveUrl; }} />
    {#if live && loaded === live && model.photo}<span class="text-[12px] text-[var(--muted)]">{Tf("Latest image of %s", chosen)}</span>{/if}
  </div>
</section>
