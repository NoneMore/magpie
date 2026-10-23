// Built-in model fingerprint detection UI. All requests go to Magpie's
// protected local management API; model provider credentials never reach JS.
magpie.plugin("fingerprint", (ctx) => {
  const html = [
    '<div class="fp-head"><div><h2 data-t>Model fingerprint</h2>',
    '<p class="fp-muted" data-t>Compare three numeric challenges with lm-detector reference models.</p></div>',
    '<button type="button" class="text" id="fpRefresh" data-t>Refresh</button></div>',
    '<section class="fp-card"><label for="fpModel" data-t>Gateway model</label>',
    '<div class="fp-select"><select id="fpModel"></select>',
    '<button type="button" id="fpStart" data-t>Run fingerprint</button></div>',
    '<p class="fp-muted" data-t>Runs against one exact provider/model. Probe requests consume tokens.</p>',
    '<p class="fp-error" id="fpError" role="alert" hidden></p></section>',
    '<section class="fp-card"><div class="fp-subtitle" data-t>Detection jobs</div>',
    '<div id="fpJobs" class="fp-jobs"><p data-t>No detections yet</p></div></section>',
    '<p class="fp-muted fp-caveat" data-t>Probabilities are relative to the closed reference set; they do not prove the model identity.</p>',
    '<p class="fp-muted fp-engine" id="fpEngine"></p>',
  ].join("");

  let view, select, runButton, refreshButton, errors, jobsNode, engineNode;
  let timer = null;
  let busy = false;
  let models = [];

  function tr(value) {
    return typeof t === "function" ? t(value) : value;
  }
  function error(message) {
    errors.textContent = String(message || "");
    errors.hidden = !message;
  }
  function elt(tag, className, value) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (value !== undefined) node.textContent = String(value);
    return node;
  }
  function percent(num) {
    return Number.isFinite(num) ? (num * 100).toFixed(1) + "%" : "—";
  }
  function modelPicker(list) {
    const chosen = select.value;
    select.replaceChildren();
    const empty = document.createElement("option");
    empty.value = "";
    empty.textContent = tr("Choose a model");
    select.append(empty);
    for (const id of list) {
      const option = document.createElement("option");
      option.value = id;
      option.textContent = id;
      select.append(option);
    }
    if (list.includes(chosen)) select.value = chosen;
    runButton.disabled = busy || !select.value;
  }
  function jobRow(job) {
    const row = elt("article", "fp-job");
    const top = elt("div", "fp-job-head");
    top.append(elt("strong", "", job.model), elt("span", "fp-job-state", tr(job.state || "")));
    row.append(top);
    if (job.startedAt) {
      const when = new Date(job.startedAt).toLocaleString();
      row.append(elt("div", "fp-muted fp-time", when));
    }
    if (job.error) row.append(elt("p", "fp-error", job.error));
    const running = job.state === "preparing" || job.state === "running";
    if (running) {
      row.append(elt("div", "fp-muted", tr("Collecting three samples…")));
      const cancel = elt("button", "text", tr("Cancel"));
      cancel.type = "button";
      cancel.onclick = async () => {
        cancel.disabled = true;
        try {
          await api("fingerprint/cancel", { id: job.id });
          await refreshJobs();
        } catch (e) {
          error(e?.message || e);
        } finally {
          cancel.disabled = false;
        }
      };
      row.append(cancel);
    }
    const result = job.analysis;
    if (result && typeof result === "object") {
      const headline = elt("div", "fp-pred");
      headline.append(
        elt("span", "fp-muted", tr("Closest model")),
        elt("strong", "", result.prediction_name || result.prediction || tr("Unscorable")),
        elt("b", "fp-pct", percent(result.probability)),
      );
      row.append(headline);
      if (result.family_prediction_name) {
        row.append(elt("div", "fp-muted", tr("Closest family") + ": " + result.family_prediction_name));
      }
      if (result.evidence?.reason) {
        row.append(elt("p", "fp-muted", String(result.evidence.reason)));
      }
      if (Array.isArray(result.results)) {
        const list = elt("ol", "fp-ranking");
        for (const entry of result.results.slice(0, 8)) {
          const item = elt("li", "fp-rank");
          item.append(
            elt("span", "fp-name", entry.display_name || entry.model || "?"),
            elt("span", "fp-pct", percent(entry.probability)),
          );
          list.append(item);
        }
        row.append(list);
      }
    }
    return row;
  }
  async function refreshJobs() {
    const data = await api("fingerprint/jobs");
    const jobs = Array.isArray(data?.jobs) ? data.jobs : [];
    jobsNode.replaceChildren();
    if (!jobs.length) jobsNode.append(elt("p", "fp-muted", tr("No detections yet")));
    for (const job of jobs) jobsNode.append(jobRow(job));
    engineNode.textContent = data?.engine ? "lmfpd " + data.engine : "";
    const waiting = jobs.some(job => job.state === "preparing" || job.state === "running");
    if (waiting && !timer) {
      timer = setInterval(() => refreshJobs().catch(e => error(e?.message || e)), 1700);
    } else if (!waiting && timer) {
      clearInterval(timer);
      timer = null;
    }
  }
  async function refreshModels() {
    const data = await api("fingerprint/models");
    models = Array.isArray(data?.models) ? data.models : [];
    modelPicker(models);
  }
  async function refresh() {
    error("");
    try {
      await Promise.all([refreshModels(), refreshJobs()]);
    } catch (e) {
      error(e?.message || e);
    }
  }
  async function start() {
    const model = select.value;
    if (!model || busy) return;
    busy = true;
    runButton.disabled = true;
    error("");
    try {
      await api("fingerprint/start", { model });
      await refreshJobs();
    } catch (e) {
      error(e?.message || e);
    } finally {
      busy = false;
      runButton.disabled = !select.value;
    }
  }
  ctx.slots.add("page", {
    key: "fingerprint",
    title: "Model fingerprint",
    html,
    mount(main) {
      view = main;
      select = view.querySelector("#fpModel");
      runButton = view.querySelector("#fpStart");
      refreshButton = view.querySelector("#fpRefresh");
      errors = view.querySelector("#fpError");
      jobsNode = view.querySelector("#fpJobs");
      engineNode = view.querySelector("#fpEngine");
      select.addEventListener("change", () => { runButton.disabled = !select.value || busy; });
      runButton.addEventListener("click", start);
      refreshButton.addEventListener("click", refresh);
    },
    open() {
      return refresh();
    },
    refresh() {
      return refresh();
    },
  });
  ctx.onDispose(() => {
    if (timer) clearInterval(timer);
    timer = null;
  });
});
