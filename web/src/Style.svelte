<script>
  // The notification style: pick a model and its options, and see the notification
  // change on the phone next to them, before sending a sample.
  import { T, i18n } from "./lib/i18n.svelte.js";
  import { app, change, sendTest } from "./lib/store.svelte.js";
  import { STYLES, cameraOverrides, effective, seconds, styleOf } from "./lib/settings.js";
  import { previewModel } from "./lib/preview.js";
  import { routeHash } from "./lib/route.js";
  import { messageTime } from "./lib/time.js";
  import photo from "./assets/sample.jpg";
  import Card from "./lib/Card.svelte";
  import Phone from "./lib/Phone.svelte";
  import Switch from "./lib/Switch.svelte";

  const eff = $derived(effective(app.overlay));
  const current = $derived(styleOf(eff));
  const inPlace = $derived(eff.media_in_place !== false);
  // What the caption shows, each with its switch.
  const SHOWN = ["show_zones", "show_score", "show_link", "show_date"];
  const shown = () => ({ show_zones: T("Zones"), show_score: T("Score"), show_link: T("“Open in Frigate” link"), show_date: T("Date") });
  const overrides = $derived(cameraOverrides(app.overlay, ["snapshot", "clip", "gif", "crop", "media_in_place", "genai_description", ...SHOWN]));
  const cams = $derived((app.data.cameras || []).map((c) => c.name).filter((n) => effective(app.overlay, n).enabled !== false));
  // The sample's camera lends it its emoji.
  const model = $derived({ ...previewModel(eff, { homeAssistant: !!app.overlay.home_assistant_url }), emoji: (cams[0] && effective(app.overlay, cams[0]).emoji) || "" });
  // The sample is dated now, written as the messages write times (the grouped lines: 15:04).
  const at = Date.now();
  const tz = $derived(app.data.timezone);
  const clock = (t) => new Date(t).toLocaleTimeString("en-GB", { timeZone: tz, hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
  const sample = $derived({
    label: "person", camera: cams[0] || T("front door"), time: messageTime(at, { tz, T, date: eff.show_date !== false }), zones: [T("driveway")], score: 0.87,
    grouped: [{ time: clock(at + 60000), label: "car", camera: cams[0] || T("front door") }, { time: clock(at + 120000), label: "dog", camera: cams[0] || T("front door") }],
  });

  const labels = () => ({
    clip: [T("Photo + video"), inPlace ? T("The image right away, then the video takes its place in the same message.") : T("The image right away, then the video clip as a reply when the event ends.")],
    photo: [T("Photo only"), T("Light. The clip stays one tap away with the 🎬 button.")],
    gif: [T("Photo + GIF"), inPlace ? T("The image, then a short animated preview in its place.") : T("A short animated preview instead of the video.")],
    text: [T("Text only"), T("The most discreet: just the message and the buttons.")],
  });
  const groups = () => [["0s", T("One message per detection")], ["2m", T("📦 Group over 2 min")], ["5m", T("📦 Group over 5 min")]];

  // On a phone the preview comes after every setting: a floating button brings it up
  // while it is out of sight.
  let preview = $state();
  let previewSeen = $state(true);
  $effect(() => {
    const o = new IntersectionObserver(([e]) => { previewSeen = e.isIntersecting; });
    o.observe(preview);
    return () => o.disconnect();
  });

  let pick = $state("");
  // The camera of the sample: the one picked while it is still enabled, else the first.
  const chosen = $derived(cams.includes(pick) ? pick : cams[0]);
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
              <span class="grid h-10 content-center gap-1 rounded-[8px] bg-[var(--glass)] px-2"><i class="h-[3px] w-4/5 rounded bg-[var(--faint)]"></i><i class="h-[3px] w-3/5 rounded bg-[var(--faint)]"></i><i class="h-[3px] w-2/5 rounded bg-[var(--faint)]"></i></span>
            {:else}
              <span class="relative h-10 overflow-hidden rounded-[8px] bg-[#26313b]">
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
        <div id="style-group" class="pills">
          {#each groups() as [value, label] (value)}
            <button type="button" class="pill" data-group={value} aria-pressed={seconds(eff.group) === seconds(value)} onclick={() => change({ group: value })}>{label}</button>
          {/each}
        </div></div>
    </Card>
    <Card>
      <div><div>{T("Show in the caption")}</div><div class="text-[12px] text-[var(--faint)]">{T("Besides the object and the camera. Without the date, the time alone.")}</div></div>
      <div id="style-shown" class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {#each SHOWN as key (key)}
          <div class="flex items-center gap-4"><div class="min-w-0 flex-1">{shown()[key]}</div>
            <Switch id={"style-" + key} checked={eff[key] !== false} label={shown()[key]} onchange={(v) => change({ [key]: v })} /></div>
        {/each}
      </div>
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
  <div id="live-preview" bind:this={preview} class="flex flex-col items-center gap-3 lg:sticky lg:top-24 lg:col-span-5 lg:self-start">
    <span class="lbl">{T("Live preview")}</span>
    <Phone {model} entry={sample} />
  </div>
  {#if !previewSeen}
    <!-- lg:hidden on the wrapper: .btn's display would win over it on the button. On the right: the save status floats in the middle.
         A round icon, not a labelled button: it covers as little of the settings as it can. -->
    <div class="fixed bottom-[max(1rem,env(safe-area-inset-bottom))] right-4 z-10 lg:hidden">
      <button type="button" id="see-preview" class="btn btn-primary btn-icon shadow-lg" aria-label={T("Live preview")} title={T("Live preview")}
        onclick={() => preview.scrollIntoView({ behavior: "smooth", block: "center" })}><span aria-hidden="true">👁</span></button>
    </div>
  {/if}
</section>
