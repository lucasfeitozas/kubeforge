# ADR 0004 — Cache de build reaproveitado por padrão; `--no-cache` como parâmetro por chamada, não campo persistido

**Status:** Aceita
**Data:** 2026-08-20
**Contexto da história:** E3.S4 — Cache de build simples

## Contexto

A E3.S4 pede dois critérios de aceite: (1) builds sucessivos do mesmo
Componente reaproveitam o cache de camadas Docker do daemon do Minikube, e
(2) uma opção explícita para forçar rebuild sem cache.

O critério (1) já estava satisfeito antes desta história, sem nenhuma
mudança de código: `DockerBuilder.Build` (ADR 0002) sempre aponta para o
mesmo daemon Docker do Minikube via `MinikubeDockerEnv.Resolve`, e o `docker
build` montado nunca passava `--no-cache` — o cache de camadas do daemon já
era reaproveitado por padrão. O trabalho real desta história é só o
critério (2).

## Decisão

### `NoCache` é um parâmetro de invocação (`BuildSpec`/`Broker.Run`), não um campo do Componente

`docs/ARCHITECTURE.md` §2.2 já documenta um `build.cacheEnabled: boolean` no
schema aspiracional do Componente, mas ele nunca foi implementado — nem em
`internal/store/component_validation.go` (que só valida `build.strategy`),
nem em `brokerBuildBlock`. Esta história **não** implementa esse campo.

Em vez disso, `BuildSpec.NoCache` e `Broker.Run(ctx, component, cloneDestDir,
noCache bool)` tratam a escolha como um parâmetro por chamada: decidida a
cada build, sem persistir no Componente. Quando a API HTTP existir (E6.S2),
o padrão natural é algo como `POST /components/{id}/build?noCache=true`.

Alternativa descartada: implementar `build.cacheEnabled` persistido no
Componente, alinhado ao schema já documentado. Rejeitada porque "forçar
rebuild sem cache" é semanticamente uma ação pontual (mesmo sentido de
`docker build --no-cache`), não uma preferência duradoura do Componente —
transformá-la em campo persistido exigiria editar o Componente para alternar
entre builds, quando o caso de uso real é "desta vez, ignore o cache".
`build.cacheEnabled` permanece intencionalmente órfão no schema; se um caso
de uso futuro pedir um default persistido por Componente, cabe a uma história
própria decidir como ele interage com o override por chamada aqui
introduzido.

`Broker.Run` recebe `noCache` como parâmetro posicional, não uma options
struct: não há esse padrão em `internal/build`/`internal/store`, e
introduzir um agora para um único bool seria abstração sem necessidade de
produção (mesma linha do ADR 0002, item 5, e do ADR 0003, item 4). `noCache`
tem o mesmo caráter de `cloneDestDir` — parâmetro da invocação, não do
domínio do Componente.

## Consequências

- `BuildSpec` ganha `NoCache bool`; com o zero value (`false`), o `docker
  build` montado é idêntico ao de antes desta história.
- `Broker.Run` muda de assinatura (`+noCache bool`) — todos os chamadores
  (hoje, só testes) precisam do novo argumento.
- Quando a API HTTP (E6.S2) ou um comando CLI existir, a exposição de
  `noCache` é só plumbing até `Broker.Run` — nenhuma mudança adicional em
  `internal/build` é esperada para isso.
