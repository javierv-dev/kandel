// Plugin de Kandel para OpenCode v2 (gestionado por `kandel install opencode` — no editar a mano).
// Conecta los eventos y hooks de ciclo de vida de OpenCode v2 (arranque, cada prompt de usuario,
// compactación y borrado de sesión) con el binario local `kandel hook oc-*` para compartir memoria
// persistente y relevos (handoff) con Claude Code, Antigravity y Pi Agent.

import { spawn } from "node:child_process";

const KANDEL_MARKER = "<!-- kandel:system-context -->";

function runKandelHook(event, payload, timeoutMs) {
	return new Promise((resolve) => {
		let stdout = "";
		let settled = false;
		const finish = (out) => {
			if (settled) return;
			settled = true;
			resolve(out);
		};

		let child;
		try {
			child = spawn("kandel", ["hook", event], {
				cwd: payload.cwd || process.cwd(),
				stdio: ["pipe", "pipe", "ignore"],
				windowsHide: true,
			});
		} catch {
			finish("");
			return;
		}

		const timer = setTimeout(() => {
			try {
				child.kill();
			} catch {
				// ignorar
			}
			finish(stdout);
		}, timeoutMs);

		child.stdout?.on("data", (chunk) => {
			stdout += chunk.toString();
		});
		child.on("error", () => {
			clearTimeout(timer);
			finish("");
		});
		child.on("close", () => {
			clearTimeout(timer);
			finish(stdout);
		});

		try {
			child.stdin?.write(JSON.stringify(payload));
			child.stdin?.end();
		} catch {
			// ignorar errores de tubería cerrada
		}
	});
}

function eventSessionID(event) {
	return (
		event?.data?.sessionID ||
		event?.properties?.sessionID ||
		event?.properties?.id ||
		event?.sessionID ||
		event?.id ||
		""
	);
}

export default {
	id: "kandel",
	async setup(ctx) {
		const cwd = ctx?.location?.directory || process.cwd();
		const controller = new AbortController();

		const startedSessions = new Set();
		const inFlightStart = new Map();
		const startupBySession = new Map();
		const compactionBySession = new Map();
		const relevantBySession = new Map();

		const clearSession = (sid) => {
			if (!sid) return;
			startedSessions.delete(sid);
			inFlightStart.delete(sid);
			startupBySession.delete(sid);
			compactionBySession.delete(sid);
			relevantBySession.delete(sid);
		};

		const ensureSessionStarted = async (sid) => {
			if (!sid) return "";
			if (startedSessions.has(sid)) {
				return startupBySession.get(sid) || "";
			}
			const pending = inFlightStart.get(sid);
			if (pending) {
				return pending;
			}
			const promise = (async () => {
				const out = (await runKandelHook("oc-session-start", { session_id: sid, cwd }, 10000)).trim();
				if (out) {
					startupBySession.set(sid, out);
				}
				startedSessions.add(sid);
				inFlightStart.delete(sid);
				return startupBySession.get(sid) || "";
			})();
			inFlightStart.set(sid, promise);
			return promise;
		};

		void (async () => {
			try {
				const stream = ctx?.event?.subscribe?.({
					types: ["session.created", "session.compaction.ended", "session.deleted"],
					signal: controller.signal,
				});
				if (!stream) return;
				for await (const event of stream) {
					const sid = eventSessionID(event);
					if (!sid) continue;
					if (event.type === "session.created") {
						await ensureSessionStarted(sid);
					} else if (event.type === "session.compaction.ended") {
						relevantBySession.delete(sid);
						const out = (await runKandelHook("oc-post-compaction", { session_id: sid, cwd }, 10000)).trim();
						if (out) {
							compactionBySession.set(sid, out);
						}
					} else if (event.type === "session.deleted") {
						await runKandelHook("oc-stop", { session_id: sid, cwd }, 5000);
						clearSession(sid);
					}
				}
			} catch {
				// La cancelación por AbortController al descargar el plugin es esperada.
			}
		})();

		if (ctx?.session?.hook) {
			await ctx.session.hook("prompt", async (event) => {
				const sid = event?.sessionID || "";
				if (!sid) return;
				await ensureSessionStarted(sid);

				const promptText = typeof event?.prompt?.text === "string" ? event.prompt.text : "";
				if (!promptText.trim()) {
					relevantBySession.delete(sid);
					return;
				}
				const raw = (await runKandelHook("oc-prompt", { session_id: sid, cwd, prompt: promptText }, 5000)).trim();
				if (!raw) {
					relevantBySession.delete(sid);
					return;
				}
				try {
					const parsed = JSON.parse(raw);
					if (typeof parsed?.additionalContext === "string" && parsed.additionalContext.trim()) {
						relevantBySession.set(sid, parsed.additionalContext.trim());
					} else {
						relevantBySession.delete(sid);
					}
				} catch {
					relevantBySession.delete(sid);
				}
			});

			await ctx.session.hook("context", async (event) => {
				const sid = event?.sessionID || "";
				if (!sid || !Array.isArray(event?.system)) return;

				const startupText = await ensureSessionStarted(sid);
				const compactionText = compactionBySession.get(sid) || "";
				if (compactionText) {
					compactionBySession.delete(sid);
				}
				const relevantText = relevantBySession.get(sid) || "";

				const parts = [startupText, compactionText, relevantText].filter(Boolean);
				const idx = event.system.findIndex(
					(part) => typeof part?.text === "string" && part.text.startsWith(KANDEL_MARKER),
				);

				if (parts.length === 0) {
					if (idx >= 0) event.system.splice(idx, 1);
					return;
				}

				const block = { type: "text", text: `${KANDEL_MARKER}\n${parts.join("\n\n")}` };
				if (idx >= 0) {
					event.system[idx] = block;
				} else {
					event.system.push(block);
				}
			});
		}

		return () => {
			controller.abort();
			startedSessions.clear();
			inFlightStart.clear();
			startupBySession.clear();
			compactionBySession.clear();
			relevantBySession.clear();
		};
	},
};
