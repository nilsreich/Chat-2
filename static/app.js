document.addEventListener("DOMContentLoaded", () => {
  if ("serviceWorker" in navigator) navigator.serviceWorker.register("/sw.js");
  document.querySelectorAll("[data-open]").forEach((button) => button.addEventListener("click", () => document.getElementById(button.dataset.open).showModal()));
  document.querySelectorAll("[data-close]").forEach((button) => button.addEventListener("click", () => button.closest("dialog").close()));
  document.querySelectorAll("[data-confirm]").forEach((button) => button.addEventListener("click", (event) => {
    if (!window.confirm(button.dataset.confirm)) event.preventDefault();
  }));

  const form = document.querySelector("#grade-form");
  if (!form) return;

  const rows = [...document.querySelectorAll("[data-student]")];
  const status = document.querySelector("#entry-status");
  let active = Math.max(0, rows.findIndex((row) => row.classList.contains("active")));
  let saving = false;
  let requestNumber = 0;
  let keyboardValue = "";
  let undoTimer;

  function selectStudent(index) {
    if (saving || index < 0 || index >= rows.length) return;
    rows.forEach((row) => row.classList.remove("active"));
    active = index;
    rows[active].classList.add("active");
    rows[active].scrollIntoView({ block: "nearest" });
    status.textContent = `Note für ${rows[active].querySelector("span").textContent.trim()} wählen.`;
  }

  rows.forEach((row, index) => {
    row.addEventListener("click", () => selectStudent(index));
    row.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        selectStudent(index);
      }
    });
  });

  function offerUndo(auditID, savedRow) {
    let bar = document.querySelector("[data-grade-undo]");
    if (!bar) {
      bar = document.createElement("aside");
      bar.className = "undo-bar";
      bar.dataset.gradeUndo = "";
      document.body.append(bar);
    }
    bar.replaceChildren();
    const label = document.createElement("span");
    label.textContent = "Note gespeichert";
    const button = document.createElement("button");
    button.textContent = "Rückgängig";
    bar.append(label, button);
    bar.hidden = false;
    clearTimeout(undoTimer);
    button.addEventListener("click", async () => {
      button.disabled = true;
      try {
        const response = await fetch(`/changes/${auditID}/undo`, { method: "POST", body: new URLSearchParams({ ajax: "1" }) });
        if (!response.ok || response.redirected || !response.headers.get("content-type")?.includes("application/json")) throw new Error(await response.text());
        const data = await response.json();
        savedRow.querySelector("output").textContent = data.display;
        bar.hidden = true;
      } catch (_) {
        label.textContent = "Rückgängig fehlgeschlagen. Bitte Verlauf prüfen.";
        button.disabled = false;
      }
    });
    undoTimer = window.setTimeout(() => { bar.hidden = true; }, 8000);
  }

  async function save(value) {
    if (saving || !rows[active]) return;
    const savedIndex = active;
    const savedRow = rows[savedIndex];
    const studentID = savedRow.dataset.student;
    const thisRequest = ++requestNumber;
    saving = true;
    keyboardValue = "";
    status.textContent = "Speichert …";
    document.querySelectorAll("[data-grade]").forEach((button) => { button.disabled = true; });

    try {
      const response = await fetch(`${form.dataset.action}${studentID}`, {
        method: "POST",
        body: new URLSearchParams({ points: value }),
        headers: { Accept: "application/json" },
      });
      const contentType = response.headers.get("content-type") || "";
      if (thisRequest !== requestNumber || response.redirected || !response.ok || !contentType.includes("application/json")) {
        throw new Error("Die Note konnte nicht gespeichert werden. Bitte erneut anmelden oder versuchen.");
      }
      const data = await response.json();
      if (!Number.isInteger(data.audit_id) || typeof data.display !== "string") throw new Error("Unerwartete Serverantwort");
      savedRow.querySelector("output").textContent = data.display;
      offerUndo(data.audit_id, savedRow);
      savedRow.classList.remove("active");
      if (savedIndex < rows.length - 1) {
        active = savedIndex + 1;
        rows[active].classList.add("active");
        rows[active].scrollIntoView({ block: "nearest", behavior: "smooth" });
        status.textContent = "Gespeichert ✓";
      } else {
        status.textContent = "Alle Schüler bearbeitet ✓";
      }
    } catch (_) {
      active = savedIndex;
      savedRow.classList.add("active");
      status.textContent = "Speichern fehlgeschlagen. Derselbe Schüler bleibt ausgewählt – bitte erneut versuchen.";
    } finally {
      if (thisRequest === requestNumber) saving = false;
      document.querySelectorAll("[data-grade]").forEach((button) => { button.disabled = false; });
    }
  }

  document.querySelectorAll("[data-grade]").forEach((button) => button.addEventListener("click", () => save(button.dataset.grade)));
  document.addEventListener("keydown", (event) => {
    if (saving || event.target.matches("input, textarea, select")) return;
    if (/^\d$/.test(event.key)) {
      const candidate = keyboardValue + event.key;
      if (Number(candidate) <= 15 && candidate.length <= 2) keyboardValue = candidate;
      status.textContent = keyboardValue ? `Eingabe: ${keyboardValue} – mit Enter bestätigen.` : "Zahl zwischen 0 und 15 eingeben.";
      event.preventDefault();
    } else if (event.key === "Backspace") {
      keyboardValue = keyboardValue.slice(0, -1);
      status.textContent = keyboardValue ? `Eingabe: ${keyboardValue} – mit Enter bestätigen.` : "Note wählen.";
      event.preventDefault();
    } else if (event.key === "Escape") {
      keyboardValue = "";
      status.textContent = "Eingabe verworfen.";
    } else if (event.key === "Enter" && keyboardValue !== "") {
      const value = keyboardValue;
      keyboardValue = "";
      save(value);
      event.preventDefault();
    } else if (event.key === "ArrowDown") {
      selectStudent(Math.min(active + 1, rows.length - 1));
    } else if (event.key === "ArrowUp") {
      selectStudent(Math.max(active - 1, 0));
    }
  });
});
