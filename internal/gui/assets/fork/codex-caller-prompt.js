// Trusted fork-only GUI preference. Uses the host's narrow agentPrefs
// registry instead of editing app.js's upstream-owned AGENT_PREFS array.
(() => {
  const title = "Caller-owned Codex instructions (experimental)";
  const description = "Use the client's top-level instructions instead of Codex's native prompt on ChatGPT subscription requests. Unsupported upstream; may be refused or risk your account. Avoid switching mid-conversation.";
  magpie.strings({
    "zh": {
      "Caller-owned Codex instructions (experimental)": "调用方自定义 Codex 指令（实验性）",
      "Use the client's top-level instructions instead of Codex's native prompt on ChatGPT subscription requests. Unsupported upstream; may be refused or risk your account. Avoid switching mid-conversation.": "在 ChatGPT 订阅请求中使用客户端的顶层指令替代 Codex 原生提示词。上游不支持，可能拒绝请求或带来账号风险。不要在对话中途切换。"
    },
    "ja": {
      "Caller-owned Codex instructions (experimental)": "呼び出し元指定の Codex 指示（実験的）",
      "Use the client's top-level instructions instead of Codex's native prompt on ChatGPT subscription requests. Unsupported upstream; may be refused or risk your account. Avoid switching mid-conversation.": "ChatGPT サブスクリプションのリクエストで、Codex の標準プロンプトではなくクライアントの最上位の指示を使います。上流ではサポートされず、拒否やアカウント上のリスクがあります。会話の途中で切り替えないでください。"
    },
    "de": {
      "Caller-owned Codex instructions (experimental)": "Codex-Anweisungen des Clients (experimentell)",
      "Use the client's top-level instructions instead of Codex's native prompt on ChatGPT subscription requests. Unsupported upstream; may be refused or risk your account. Avoid switching mid-conversation.": "Für ChatGPT-Abonnementanfragen werden die obersten Anweisungen des Clients statt des nativen Codex-Prompts verwendet. Upstream nicht unterstützt; Anfragen können abgelehnt werden und es besteht ein Kontorisiko. Nicht während einer Unterhaltung umschalten."
    }
  });
  magpie.agentPrefs.register("codex", {
    id: "codexCallerPromptRow",
    controlId: "codexCallerPromptSegs",
    before: "codexAgentsV1Row",
    title, description,
    setting: "codexCallerPromptOn",
    endpoint: "settings/codex-caller-prompt",
  });
})();
