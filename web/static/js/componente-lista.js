// Listagem de Componentes com status e ações (E7.S3, ADR 0020): busca
// GET /components periodicamente e renderiza uma linha por Componente, com
// badge de status.phase, botões Build/Run/Cleanup ligados aos endpoints
// POST /components/{id}/build|run|cleanup e um link Logs para a tela de
// logs em tempo real (E7.S4, ADR 0021).
(function () {
  "use strict";

  var IN_FLIGHT_PHASES = ["Building", "Running"];
  var BADGE_CLASS = {
    Pending: "bg-secondary",
    Building: "bg-info text-dark",
    Built: "bg-primary",
    Running: "bg-info text-dark",
    Succeeded: "bg-success",
    Failed: "bg-danger",
    CleanedUp: "bg-light text-dark border",
  };
  var ACTION_LABEL = {
    build: "Building…",
    run: "Running…",
    cleanup: "Limpando…",
  };
  var POLL_INTERVAL_MS = 5000;

  document.addEventListener("DOMContentLoaded", function () {
    var tbody = document.getElementById("componentes-tbody");
    var rowTemplate = document.getElementById("componente-row-template");
    var refreshButton = document.getElementById("refresh-list");
    var listErrorAlert = document.getElementById("list-error-alert");
    var emptyState = document.getElementById("empty-state");
    var loadingState = document.getElementById("loading-state");
    if (!tbody || !rowTemplate) {
      return;
    }

    var firstLoadDone = false;
    // Mensagens de erro por Componente (id -> texto), sobrevivendo a
    // renderList(): sem isso, o refreshList() disparado logo após uma ação
    // falhar reconstruiria a linha a partir do template antes do usuário
    // conseguir ler o erro (renderList sempre recria linhas não-ocupadas).
    var rowErrorMessages = {};

    function phaseToBadgeClass(phase) {
      return BADGE_CLASS[phase] || "bg-secondary";
    }

    function phaseIsInFlight(phase) {
      return IN_FLIGHT_PHASES.indexOf(phase) !== -1;
    }

    function truncateDigest(digest) {
      return digest.length > 12 ? digest.slice(0, 12) + "…" : digest;
    }

    function showListError(message) {
      listErrorAlert.textContent = message;
      listErrorAlert.classList.remove("d-none");
    }

    function clearListError() {
      listErrorAlert.classList.add("d-none");
    }

    function applyRowError(tr, id) {
      var el = tr.querySelector('[data-col="row-error"]');
      var message = rowErrorMessages[id];
      if (message) {
        el.textContent = message;
        el.classList.remove("d-none");
      } else {
        el.textContent = "";
        el.classList.add("d-none");
      }
    }

    function showRowError(tr, id, message) {
      rowErrorMessages[id] = message;
      applyRowError(tr, id);
    }

    function clearRowError(tr, id) {
      delete rowErrorMessages[id];
      applyRowError(tr, id);
    }

    // --- Construção/atualização de linha ---

    function buildRow(component) {
      var fragment = rowTemplate.content.cloneNode(true);
      var tr = fragment.querySelector("tr");
      tr.dataset.rowId = component.id;
      tr.dataset.busy = "false";
      fillRow(tr, component);
      applyRowError(tr, component.id);
      return tr;
    }

    function fillRow(tr, component) {
      var status = component.status || { phase: "Pending" };
      var phase = status.phase;

      tr.querySelector('[data-col="nome"]').textContent = component.nome;
      tr.querySelector('[data-col="descricao"]').textContent = component.descricao || "—";

      var badge = tr.querySelector('[data-col="badge"]');
      badge.className = "badge " + phaseToBadgeClass(phase);
      badge.innerHTML = "";
      if (phaseIsInFlight(phase)) {
        var spinner = document.createElement("span");
        spinner.className = "spinner-border spinner-border-sm me-1";
        spinner.setAttribute("role", "status");
        spinner.setAttribute("aria-hidden", "true");
        badge.appendChild(spinner);
      }
      badge.appendChild(document.createTextNode(phase));

      var detalhe = tr.querySelector('[data-col="detalhe"]');
      detalhe.className = "small";
      if (phase === "Failed" && status.errorMessage) {
        detalhe.classList.add("text-danger");
        detalhe.textContent = status.errorMessage;
        detalhe.removeAttribute("title");
      } else if (status.buildImageDigest) {
        detalhe.classList.add("text-body-secondary");
        detalhe.textContent = truncateDigest(status.buildImageDigest);
        detalhe.title = status.buildImageDigest;
      } else {
        detalhe.classList.add("text-body-secondary");
        detalhe.textContent = "—";
        detalhe.removeAttribute("title");
      }

      var runButton = tr.querySelector('[data-action="run"]');
      runButton.disabled = phase !== "Built";

      var logsLink = tr.querySelector('[data-link="logs"]');
      logsLink.href = "/componentes/logs.html?id=" + encodeURIComponent(component.id);
    }

    // --- Estado de "ocupado" por linha (independente entre linhas) ---

    function setRowBusy(tr, id, action) {
      tr.dataset.busy = "true";
      clearRowError(tr, id);
      tr.querySelectorAll("[data-action]").forEach(function (btn) {
        btn.disabled = true;
      });
      var busyButton = tr.querySelector('[data-action="' + action + '"]');
      busyButton.innerHTML =
        '<span class="spinner-border spinner-border-sm" role="status" aria-hidden="true"></span> ' +
        ACTION_LABEL[action];
    }

    function clearRowBusy(tr) {
      tr.dataset.busy = "false";
    }

    // --- Ações: build/run/cleanup ---

    function runAction(tr, id, action) {
      setRowBusy(tr, id, action);

      fetch("/components/" + id + "/" + action, { method: "POST" })
        .then(function (response) {
          if (response.status === 200 || response.status === 202) {
            return response.json().then(function (body) {
              clearRowError(tr, id);
              // Atualização otimista: build/run devolvem {componentId, phase}
              // imediatamente; cleanup não tem "phase" nesse corpo — nesse
              // caso o refreshList() logo abaixo é quem traz o estado real.
              if (body && body.phase) {
                fillRow(tr, { id: id, nome: tr.querySelector('[data-col="nome"]').textContent, descricao: "", status: { phase: body.phase } });
              }
            });
          }
          // 404/409/500 desses três endpoints são texto puro (http.Error),
          // não JSON — diferente de POST /components (cadastro). Chamar
          // response.json() aqui mascararia a mensagem real com um erro de
          // parse.
          return response.text().then(function (text) {
            throw new Error(text || "Erro " + response.status + " ao executar ação.");
          });
        })
        .catch(function (err) {
          showRowError(tr, id, err.message);
        })
        .finally(function () {
          clearRowBusy(tr);
          refreshList();
        });
    }

    tbody.addEventListener("click", function (event) {
      var button = event.target.closest("[data-action]");
      if (!button || button.disabled) {
        return;
      }
      var tr = button.closest("tr");
      runAction(tr, tr.dataset.rowId, button.dataset.action);
    });

    // --- Carregamento da lista ---

    function fetchComponents() {
      return fetch("/components").then(function (response) {
        if (!response.ok) {
          throw new Error("Erro " + response.status + " ao carregar componentes.");
        }
        return response.json();
      });
    }

    function renderList(components) {
      var busyRows = {};
      tbody.querySelectorAll("tr[data-row-id]").forEach(function (tr) {
        if (tr.dataset.busy === "true") {
          busyRows[tr.dataset.rowId] = tr;
        }
      });

      tbody.textContent = "";
      components.forEach(function (component) {
        var busyRow = busyRows[component.id];
        tbody.appendChild(busyRow || buildRow(component));
      });

      emptyState.classList.toggle("d-none", tbody.children.length > 0);
    }

    function refreshList() {
      fetchComponents()
        .then(function (components) {
          clearListError();
          renderList(components);
        })
        .catch(function (err) {
          showListError(err.message);
        })
        .finally(function () {
          if (!firstLoadDone) {
            firstLoadDone = true;
            loadingState.classList.add("d-none");
          }
        });
    }

    refreshButton.addEventListener("click", refreshList);

    refreshList();
    setInterval(function () {
      if (document.visibilityState === "visible") {
        refreshList();
      }
    }, POLL_INTERVAL_MS);
  });
})();
