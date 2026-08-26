// Validadores reutilizáveis para o formulário de cadastro de Componente
// (E7.S2, ADR 0019) — cobrem regras que os atributos HTML5 nativos
// (required/type/pattern) não conseguem expressar sozinhos: quantidades
// Kubernetes, "ao menos um de N campos" e obrigatoriedade condicional.
window.KFValidation = (function () {
  "use strict";

  // Ex.: "250m", "256Mi", "1", "2Gi" — mesmo formato aceito por
  // resource.ParseQuantity no backend (internal/controller/job_builder.go).
  var QUANTITY_PATTERN = /^[0-9]+(\.[0-9]+)?(m|Ki|Mi|Gi|Ti|Pi|Ei|K|M|G|T|P|E)?$/;

  function isQuantity(value) {
    return QUANTITY_PATTERN.test(value.trim());
  }

  // Marca um <input>/<select> como inválido, exibindo `message` no
  // <div class="invalid-feedback"> irmão mais próximo.
  function markInvalid(input, message) {
    input.classList.add("is-invalid");
    var feedback = input.parentElement.querySelector(".invalid-feedback");
    if (feedback) {
      feedback.textContent = message;
    }
  }

  function clearInvalid(input) {
    input.classList.remove("is-invalid");
  }

  return {
    isQuantity: isQuantity,
    markInvalid: markInvalid,
    clearInvalid: clearInvalid,
  };
})();
