// The main interface: the overview and the notification style. The other screens are
// still in the classic interface (/classic) until they move here.
import "./app.css";
import { mount } from "svelte";
import App from "./App.svelte";

mount(App, { target: document.getElementById("app") });
