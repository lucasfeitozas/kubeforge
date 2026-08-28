// Logs em tempo real de um Componente (E7.S4, ADR 0021): busca
// GET /components/{id} uma vez para o cabeçalho (nome/fase) e consome
// GET /components/{id}/logs?follow=true via EventSource nativo, que já
// implementa o framing text/event-stream definido pela ADR 0016.
(function () {
  "use strict";

  document.addEventListener("DOMContentLoaded", function () {
    var nomeEl = document.getElementById("componente-nome");
    var faseEl = document.getElementById("componente-fase");
    var headerErrorAlert = document.getElementById("header-error-alert");
    var statusEl = document.getElementById("connection-status");
    var toggleButton = document.getElementById("toggle-autoscroll");
    var reconnectButton = document.getElementById("reconnect");
    var logViewer = document.getElementById("log-viewer");
    var emptyState = document.getElementById("log-empty-state");
    if (!logViewer) {
      return;
    }

    var id = new URLSearchParams(window.location.search).get("id");
    var autoScrollPaused = false;
    var eventSource = null;

    function showHeaderError(message) {
      headerErrorAlert.textContent = message;
      headerErrorAlert.classList.remove("d-none");
    }

    function loadComponentHeader() {
      fetch("/components/" + id)
        .then(function (response) {
          if (!response.ok) {
            // GET /components/{id} devolve erro em texto puro (não JSON),
            // mesmo padrão das ações em componente-lista.js.
            return response.text().then(function (text) {
              throw new Error(text || "Erro " + response.status + " ao carregar o Componente.");
            });
          }
          return response.json();
        })
        .then(function (component) {
          nomeEl.textContent = component.nome;
          document.title = component.nome + " — Logs — KubeForge Console";
          faseEl.textContent = (component.status && component.status.phase) || "Pending";
        })
        .catch(function (err) {
          showHeaderError(err.message);
        });
    }

    function appendLogLine(text) {
      if (emptyState) {
        emptyState.remove();
        emptyState = null;
      }
      logViewer.appendChild(document.createTextNode(text + "\n"));
      if (!autoScrollPaused) {
        logViewer.scrollTop = logViewer.scrollHeight;
      }
    }

    function setConnectionStatus(text) {
      statusEl.textContent = text;
    }

    function closeEventSource() {
      if (eventSource) {
        eventSource.close();
        eventSource = null;
      }
    }

    function connect() {
      closeEventSource();
      reconnectButton.classList.add("d-none");
      setConnectionStatus("Conectando…");

      eventSource = new EventSource("/components/" + id + "/logs?follow=true");

      eventSource.onopen = function () {
        setConnectionStatus("Conectado");
      };

      eventSource.onmessage = function (event) {
        appendLogLine(event.data);
      };

      // EventSource tenta reconectar sozinho por padrão ao perder a conexão,
      // mas o protocolo (ADR 0016) não tem id:/cursor de continuidade — uma
      // reconexão automática reabriria o snapshot inteiro e duplicaria
      // linhas já exibidas (ex.: assim que o Pod chega a uma fase
      // terminal e o servidor fecha a resposta). Por isso fechamos
      // explicitamente e deixamos a reconexão a cargo do usuário.
      eventSource.onerror = function () {
        closeEventSource();
        setConnectionStatus("Conexão encerrada");
        reconnectButton.classList.remove("d-none");
      };
    }

    toggleButton.addEventListener("click", function () {
      autoScrollPaused = !autoScrollPaused;
      toggleButton.textContent = autoScrollPaused ? "Retomar auto-scroll" : "Pausar auto-scroll";
      if (!autoScrollPaused) {
        logViewer.scrollTop = logViewer.scrollHeight;
      }
    });

    reconnectButton.addEventListener("click", connect);

    if (!id) {
      showHeaderError("Nenhum Componente informado na URL.");
      setConnectionStatus("—");
      return;
    }

    loadComponentHeader();
    connect();
  });
})();
