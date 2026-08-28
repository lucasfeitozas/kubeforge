// Formulário de cadastro de Componente (E7.S2, ADR 0019): monta o JSON
// aninhado esperado por POST /components a partir dos campos do form,
// valida no cliente antes do submit, e mapeia erros 400 do servidor de
// volta para os campos via [data-field].
(function () {
  "use strict";

  document.addEventListener("DOMContentLoaded", function () {
    var form = document.getElementById("componente-form");
    if (!form) {
      return;
    }

    var summaryAlert = document.getElementById("form-summary-alert");
    var successAlert = document.getElementById("form-success-alert");
    var storageTypeSelect = document.getElementById("field-storageType");
    var ephemeralFields = document.getElementById("storage-ephemeral-fields");
    var pvcFields = document.getElementById("storage-pvc-fields");
    var pvcSizeInput = document.getElementById("field-pvcSize");
    var requestsCpuInput = document.getElementById("field-requestsCpu");
    var requestsMemoryInput = document.getElementById("field-requestsMemory");
    var requestsGroupFeedback = document.getElementById("requests-group-feedback");
    var envRowsContainer = document.getElementById("env-rows");
    var addEnvRowButton = document.getElementById("add-env-row");
    var envRowTemplate = document.getElementById("env-row-template");

    function field(dataField) {
      return form.querySelector('[data-field="' + dataField + '"]');
    }

    // --- Storage: mostra/esconde blocos conforme resources.storage.type ---

    function updateStorageFieldsVisibility() {
      var type = storageTypeSelect.value;
      ephemeralFields.classList.toggle("d-none", type !== "ephemeral");
      pvcFields.classList.toggle("d-none", type !== "pvc");
      if (type !== "pvc") {
        pvcSizeInput.classList.remove("is-invalid");
      }
    }
    storageTypeSelect.addEventListener("change", updateStorageFieldsVisibility);
    updateStorageFieldsVisibility();

    // --- Env vars: linhas repetíveis ---

    function addEnvRow() {
      var fragment = envRowTemplate.content.cloneNode(true);
      var removeBtn = fragment.querySelector("[data-remove-env]");
      var row = fragment.querySelector(".kf-env-row");
      removeBtn.addEventListener("click", function () {
        row.remove();
      });
      envRowsContainer.appendChild(fragment);
    }
    addEnvRowButton.addEventListener("click", addEnvRow);

    function collectEnvVars() {
      var env = [];
      envRowsContainer.querySelectorAll(".kf-env-row").forEach(function (row) {
        var name = row.querySelector("[data-env-name]").value.trim();
        var value = row.querySelector("[data-env-value]").value.trim();
        if (name || value) {
          env.push({ name: name, value: value });
        }
      });
      return env;
    }

    // --- Validação customizada (regras que HTML5 não expressa) ---

    function runCustomValidation() {
      var valid = true;

      var hasCpu = requestsCpuInput.value.trim() !== "";
      var hasMemory = requestsMemoryInput.value.trim() !== "";
      if (!hasCpu && !hasMemory) {
        requestsGroupFeedback.classList.remove("d-none");
        requestsCpuInput.classList.add("is-invalid");
        requestsMemoryInput.classList.add("is-invalid");
        valid = false;
      } else {
        requestsGroupFeedback.classList.add("d-none");
      }

      if (storageTypeSelect.value === "pvc" && pvcSizeInput.value.trim() === "") {
        window.KFValidation.markInvalid(pvcSizeInput, "Obrigatório quando o tipo de storage é PVC.");
        valid = false;
      }

      envRowsContainer.querySelectorAll(".kf-env-row").forEach(function (row) {
        var nameInput = row.querySelector("[data-env-name]");
        var valueInput = row.querySelector("[data-env-value]");
        var name = nameInput.value.trim();
        var value = valueInput.value.trim();
        if ((name && !value) || (!name && value)) {
          nameInput.classList.add("is-invalid");
          valueInput.classList.add("is-invalid");
          valid = false;
        } else {
          nameInput.classList.remove("is-invalid");
          valueInput.classList.remove("is-invalid");
        }
      });

      return valid;
    }

    // --- Montagem do payload aninhado ---

    function splitCommaList(value) {
      return value
        .split(",")
        .map(function (item) { return item.trim(); })
        .filter(function (item) { return item !== ""; });
    }

    function assemblePayload() {
      var payload = {
        nome: field("nome").value.trim(),
      };

      var descricao = field("descricao").value.trim();
      if (descricao) payload.descricao = descricao;

      payload.source = {
        repoUrl: field("source.repoUrl").value.trim(),
        ref: { type: field("source.ref.type").value },
      };
      var refValue = field("source.ref.value").value.trim();
      if (refValue) payload.source.ref.value = refValue;
      var credentialsSecretRef = field("source.credentialsSecretRef").value.trim();
      if (credentialsSecretRef) payload.source.credentialsSecretRef = credentialsSecretRef;

      var buildStrategy = field("build.strategy").value;
      var dockerfilePath = field("build.dockerfilePath").value.trim();
      var cacheEnabled = field("build.cacheEnabled").checked;
      if (buildStrategy || dockerfilePath || cacheEnabled) {
        payload.build = {};
        if (buildStrategy) payload.build.strategy = buildStrategy;
        if (dockerfilePath) payload.build.dockerfilePath = dockerfilePath;
        if (cacheEnabled) payload.build.cacheEnabled = true;
      }

      var requests = {};
      if (requestsCpuInput.value.trim()) requests.cpu = requestsCpuInput.value.trim();
      if (requestsMemoryInput.value.trim()) requests.memory = requestsMemoryInput.value.trim();
      payload.resources = { requests: requests };

      var limits = {};
      var limitsCpu = field("resources.limits.cpu").value.trim();
      var limitsMemory = field("resources.limits.memory").value.trim();
      if (limitsCpu) limits.cpu = limitsCpu;
      if (limitsMemory) limits.memory = limitsMemory;
      if (Object.keys(limits).length > 0) payload.resources.limits = limits;

      var storageType = storageTypeSelect.value;
      if (storageType === "ephemeral") {
        var sizeLimit = field("resources.storage.sizeLimit").value.trim();
        payload.resources.storage = { type: "ephemeral" };
        if (sizeLimit) payload.resources.storage.sizeLimit = sizeLimit;
      } else if (storageType === "pvc") {
        var pvc = { size: pvcSizeInput.value.trim() };
        var storageClassName = field("resources.storage.pvc.storageClassName").value.trim();
        if (storageClassName) pvc.storageClassName = storageClassName;
        var accessModes = Array.prototype.slice
          .call(document.querySelectorAll('#storage-pvc-fields input[type="checkbox"]:checked'))
          .map(function (el) { return el.value; });
        if (accessModes.length > 0) pvc.accessModes = accessModes;
        payload.resources.storage = { type: "pvc", pvc: pvc };
      }

      payload.runtime = {
        workloadKind: field("runtime.workloadKind").value,
      };
      var logLevel = field("runtime.logLevel").value.trim();
      if (logLevel) payload.runtime.logLevel = logLevel;
      var backoffLimit = field("runtime.backoffLimit").value.trim();
      if (backoffLimit) payload.runtime.backoffLimit = parseInt(backoffLimit, 10);
      var command = splitCommaList(field("runtime.command").value);
      if (command.length > 0) payload.runtime.command = command;
      var args = splitCommaList(field("runtime.args").value);
      if (args.length > 0) payload.runtime.args = args;
      var env = collectEnvVars();
      if (env.length > 0) payload.runtime.env = env;

      payload.targetContext = {
        cluster: field("targetContext.cluster").value,
      };
      var namespace = field("targetContext.namespace").value.trim();
      if (namespace) payload.targetContext.namespace = namespace;
      var kubeconfigSecretRef = field("targetContext.kubeconfigSecretRef").value.trim();
      if (kubeconfigSecretRef) payload.targetContext.kubeconfigSecretRef = kubeconfigSecretRef;

      var lifecycle = {};
      var ttl = field("lifecycle.ttlSecondsAfterFinished").value.trim();
      if (ttl) lifecycle.ttlSecondsAfterFinished = parseInt(ttl, 10);
      var deadline = field("lifecycle.activeDeadlineSeconds").value.trim();
      if (deadline) lifecycle.activeDeadlineSeconds = parseInt(deadline, 10);
      var cleanupPolicy = field("lifecycle.cleanupPolicy").value;
      if (cleanupPolicy) lifecycle.cleanupPolicy = cleanupPolicy;
      if (Object.keys(lifecycle).length > 0) payload.lifecycle = lifecycle;

      return payload;
    }

    // --- Erros do servidor (400) mapeados de volta para os campos ---

    function showServerErrors(errors) {
      var unmapped = 0;
      errors.forEach(function (err) {
        var input = field(err.field);
        if (input) {
          window.KFValidation.markInvalid(input, err.message);
        } else {
          unmapped++;
        }
      });
      var message = "Não foi possível cadastrar o Componente (" + errors.length + " erro(s)).";
      if (unmapped > 0) {
        message += " Alguns erros não puderam ser associados a um campo específico — veja o console.";
        console.error("Erros de validação não mapeados:", errors);
      }
      summaryAlert.textContent = message;
      summaryAlert.classList.remove("d-none");
    }

    // --- Submit ---

    form.addEventListener("submit", function (event) {
      event.preventDefault();
      event.stopPropagation();

      summaryAlert.classList.add("d-none");
      successAlert.classList.add("d-none");
      form.querySelectorAll(".is-invalid").forEach(function (el) {
        el.classList.remove("is-invalid");
      });

      var nativeValid = form.checkValidity();
      var customValid = runCustomValidation();
      form.classList.add("was-validated");

      if (!nativeValid || !customValid) {
        var firstInvalid = form.querySelector(".is-invalid, :invalid");
        if (firstInvalid) firstInvalid.focus();
        summaryAlert.textContent = "Corrija os campos destacados antes de enviar.";
        summaryAlert.classList.remove("d-none");
        return;
      }

      var payload = assemblePayload();

      fetch("/components", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      })
        .then(function (response) {
          if (response.status === 201) {
            successAlert.classList.remove("d-none");
            form.reset();
            form.classList.remove("was-validated");
            envRowsContainer.innerHTML = "";
            updateStorageFieldsVisibility();
            return null;
          }
          if (response.status === 400) {
            return response.json().then(function (body) {
              showServerErrors(body.errors || []);
            });
          }
          throw new Error("Resposta inesperada do servidor: " + response.status);
        })
        .catch(function (err) {
          console.error(err);
          summaryAlert.textContent = "Erro ao cadastrar componente. Tente novamente.";
          summaryAlert.classList.remove("d-none");
        });
    });
  });
})();
