// Extensión de Kandel para Pi Agent (gestionada por `kandel install pi` — no editar a mano).
// Conecta los eventos de ciclo de vida de Pi (arranque, cada turno de usuario, compactación y cierre)
// con el binario local `kandel hook pi-*` para compartir memoria persistente y relevos (handoff)
// con Claude Code y Antigravity.

import { spawn } from "node:child_process";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

interface HookPayload {
	session_id: string;
	cwd: string;
	prompt?: string;
	transcript_path?: string;
}

function runKandelHook(event: string, payload: HookPayload, timeoutMs: number): Promise<string> {
	return new Promise((resolve) => {
		let stdout = "";
		let settled = false;
		const finish = (out: string) => {
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

		child.stdout?.on("data", (chunk: Buffer | string) => {
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

function buildPayload(ctx: ExtensionContext, prompt?: string): HookPayload {
	return {
		session_id: ctx.sessionManager.getSessionId() ?? "",
		cwd: ctx.cwd,
		prompt,
		transcript_path: ctx.sessionManager.getSessionFile() ?? "",
	};
}

export default function kandelExtension(pi: ExtensionAPI) {
	let initializedSessionId = "";
	let startupContextBlock = "";
	let pendingCompactionBlock = "";

	const ensureSessionStarted = async (ctx: ExtensionContext) => {
		const sid = ctx.sessionManager.getSessionId() ?? "";
		if (!sid || initializedSessionId === sid) {
			return;
		}
		initializedSessionId = sid;
		const out = await runKandelHook("pi-session-start", buildPayload(ctx), 10000);
		if (out.trim()) {
			startupContextBlock = out.trim();
		}
	};

	pi.on("session_start", async (_event, ctx) => {
		initializedSessionId = "";
		startupContextBlock = "";
		pendingCompactionBlock = "";
		await ensureSessionStarted(ctx);
	});

	pi.on("session_compact", async (_event, ctx) => {
		const out = await runKandelHook("pi-post-compaction", buildPayload(ctx), 10000);
		if (out.trim()) {
			pendingCompactionBlock = out.trim();
		}
	});

	pi.on("before_agent_start", async (event, ctx) => {
		await ensureSessionStarted(ctx);

		if (startupContextBlock && event.systemPromptOptions?.sections) {
			event.systemPromptOptions.sections.kandel_memory = startupContextBlock;
		}

		const injectedParts: string[] = [];
		if (pendingCompactionBlock) {
			injectedParts.push(pendingCompactionBlock);
			pendingCompactionBlock = "";
		}

		if (event.messageType === "user" && event.prompt) {
			const raw = await runKandelHook("pi-prompt", buildPayload(ctx, event.prompt), 5000);
			if (raw.trim()) {
				try {
					const parsed = JSON.parse(raw) as { additionalContext?: string };
					if (parsed.additionalContext && parsed.additionalContext.trim()) {
						injectedParts.push(parsed.additionalContext.trim());
					}
				} catch {
					// ignorar JSON malformado: un hook nunca debe bloquear al usuario
				}
			}
		}

		if (injectedParts.length > 0) {
			return {
				message: {
					customType: "kandel-context",
					content: injectedParts.join("\n\n"),
					display: false,
				},
			};
		}
	});

	pi.on("session_shutdown", async (_event, ctx) => {
		await runKandelHook("pi-stop", buildPayload(ctx), 5000);
	});
}
