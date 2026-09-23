// Package admin tem os comandos de cadastro usados pelo operador (sem painel nesta etapa).
package admin

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

const usage = `uso:
  admin org-create --name NOME --max-budget 100 [--niche NICHO] [--objectives OUTCOME_ENGAGEMENT,OUTCOME_LEADS]
  admin member-add --org ID --name NOME --role owner|approver|viewer --phone 5511999998888 [--provider openwa]
  admin resolve-human --phone 5511999998888 [--provider openwa]`

func Run(ctx context.Context, st *store.Store, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "org-create":
		return orgCreate(ctx, st, args[1:], out)
	case "member-add":
		return memberAdd(ctx, st, args[1:], out)
	case "resolve-human":
		return resolveHuman(ctx, st, args[1:], out)
	default:
		return fmt.Errorf("comando desconhecido %q\n%s", args[0], usage)
	}
}

func orgCreate(ctx context.Context, st *store.Store, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("org-create", flag.ContinueOnError)
	fs.SetOutput(out)
	name := fs.String("name", "", "nome da organização")
	niche := fs.String("niche", "", "nicho do negócio")
	maxBudget := fs.Float64("max-budget", 0, "limite de investimento diário em reais")
	objectives := fs.String("objectives", "OUTCOME_ENGAGEMENT", "objetivos permitidos, separados por vírgula")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*name) == "" || *maxBudget <= 0 {
		return errors.New("org-create: --name e --max-budget (maior que zero) são obrigatórios")
	}
	var objs []string
	for _, o := range strings.Split(*objectives, ",") {
		if o = strings.TrimSpace(o); o != "" {
			objs = append(objs, o)
		}
	}
	if len(objs) == 0 {
		return errors.New("org-create: informe ao menos um objetivo")
	}
	id, err := st.CreateOrganization(ctx, *name, *niche, *maxBudget, objs)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "organização criada: id=%d\n", id)
	return nil
}

func memberAdd(ctx context.Context, st *store.Store, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("member-add", flag.ContinueOnError)
	fs.SetOutput(out)
	org := fs.Int64("org", 0, "id da organização")
	name := fs.String("name", "", "nome do membro")
	role := fs.String("role", "", "owner, approver ou viewer")
	phone := fs.String("phone", "", "telefone de WhatsApp")
	provider := fs.String("provider", "openwa", "provedor de WhatsApp")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *org <= 0 || strings.TrimSpace(*name) == "" {
		return errors.New("member-add: --org e --name são obrigatórios")
	}
	switch *role {
	case "owner", "approver", "viewer":
	default:
		return fmt.Errorf("member-add: papel %q inválido (use owner, approver ou viewer)", *role)
	}
	p, err := whatsapp.NormalizePhone(*phone)
	if err != nil {
		return fmt.Errorf("member-add: %w", err)
	}
	if _, err := st.FindIdentity(ctx, *provider, p); err == nil {
		return fmt.Errorf("member-add: telefone %s já cadastrado", p)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	id, err := st.CreateMember(ctx, *org, *name, *role)
	if err != nil {
		return err
	}
	if err := st.LinkPhone(ctx, *provider, p, id); err != nil {
		return err
	}
	fmt.Fprintf(out, "membro criado: id=%d telefone=%s\n", id, p)
	return nil
}

func resolveHuman(ctx context.Context, st *store.Store, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("resolve-human", flag.ContinueOnError)
	fs.SetOutput(out)
	phone := fs.String("phone", "", "telefone do cliente")
	provider := fs.String("provider", "openwa", "provedor de WhatsApp")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := whatsapp.NormalizePhone(*phone)
	if err != nil {
		return fmt.Errorf("resolve-human: %w", err)
	}
	ident, err := st.FindIdentity(ctx, *provider, p)
	if err != nil {
		return fmt.Errorf("resolve-human: %w", err)
	}
	if err := st.ResetConversation(ctx, ident.ID); err != nil {
		return fmt.Errorf("resolve-human: %w", err)
	}
	fmt.Fprintf(out, "conversa de %s reativada para o bot\n", p)
	return nil
}
