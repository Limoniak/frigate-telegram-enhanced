// web/src/setup.js — the setup page: the connection to Telegram, Frigate and MQTT.
import "./app.css";
import { mount } from "svelte";
import Setup from "./Setup.svelte";
import { T } from "./lib/i18n.svelte.js";

// The page's translator is reachable from the console and from the browser tests.
globalThis.T = T;

mount(Setup, { target: document.getElementById("app") });
