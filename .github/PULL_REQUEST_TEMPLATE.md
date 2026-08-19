# 🚀 Pull Request

## 📋 Descrição

<!-- Descreva de forma clara e concisa o que este PR faz -->

## 🔗 Issue Relacionada

<!-- Adicione o número da issue relacionada -->

Closes #ISSUE_NUMBER

## ✅ Lista de Tasks

<!-- Lista de commits ou tasks que fazem parte deste PR -->

| Commit Hash | Descrição                        | Status  |
| ----------- | -------------------------------- | ------- |
| `abc1234`   | Implementa autenticação JWT      | ✅ Done |
| `def5678`   | Adiciona middleware de validação | ✅ Done |
| `ghi9012`   | Configura testes unitários       | ✅ Done |
| `jkl3456`   | Atualiza documentação da API     | ✅ Done |

## 🎯 Mudanças Propostas

- [ ] Nova feature
- [ ] Bug fix
- [ ] Refatoração
- [ ] Documentação
- [ ] Testes
- [ ] Performance
- [ ] Segurança

## 🧪 Como Testar

<!-- Passos para testar as mudanças -->

1. Checkout para branch: `git checkout BRANCH_NAME`
2. Instale dependências: `npm install`
3. Execute testes: `npm test`
4. Acesse endpoint: `http://localhost:3000/api/endpoint`

## 📝 Release Notes

<!-- Descreva o que há de novo nesta versão -->

- **Autenticação**: Implementado sistema de JWT com refresh token
- **Performance**: Melhora de 40% na query de usuários
- **Segurança**: Adicionado rate limiting nas rotas públicas
- **UX**: Novo endpoint para recuperação de senha

## ⚠️ Breaking Changes

<!-- Liste quaisquer mudanças que possam quebrar compatibilidade -->

- `POST /auth/login` agora retorna `{ token, refreshToken }` em vez de apenas `{ token }`
- Endpoint `/users` agora requer header `X-API-Version: v2`
- Removido suporte para Node.js v12 (atualizar para v14+)
- Schema do banco de dados alterado: campo `username` agora é `user_name`

## 🔄 Checklist

- [ ] Testes passam localmente
- [ ] Documentação atualizada
- [ ] Mudanças testadas em ambiente de staging
- [ ] Sem warnings ou errors no console
- [ ] Código segue style guide do projeto
- [ ] PR revisado por pelo menos um dev

## 📱 Screenshots

<!-- Se aplicável, adicione prints das mudanças -->

| Antes          | Depois          |
| -------------- | --------------- |
| ![antes](link) | ![depois](link) |

## 👀 Reviewers

<!-- Solicite revisão de pessoas específicas -->

@username1 @username2

## 📌 Notas Adicionais

<!-- Qualquer informação extra relevante -->

- Esta PR depende do merge de #123
- Atenção especial ao arquivo de configuração `.env.example`
- Pendente atualização do README para refletir novas rotas

## 🏷️ Labels

<!-- Adicione labels relevantes -->

- [x] `feature`
- [ ] `bug`
- [ ] `documentation`
- [x] `backward-incompatible`

---

**🚀 Checklist para Merge**:

- [ ] Aprovação de pelo menos 2 reviewers
- [ ] Todos os checks CI passando
- [ ] Branch atualizada com a `main`
- [ ] Testes de integração executados
