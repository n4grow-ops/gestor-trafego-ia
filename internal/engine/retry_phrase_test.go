package engine

import "testing"

// Caso real: "Tenta subir agora" e "Agora suba o anúncio" caíam na Atendente, que mandava o link da conta.
func TestParseCampaignCommandRetryPhrases(t *testing.T) {
	for _, s := range []string{"Tenta subir agora", "Agora suba o anúncio", "tenta de novo", "Tenta de novo!", "sobe a campanha",
		"pode subir", "suba o anúncio de novo", "publica por favor", "Pode publicar a campanha", "tenta subir de novo",
		"agora sobe", "Tente novamente", "Então pode subir o anúncio agora"} {
		if cmd, _ := parseCampaignCommand(s); cmd != cmdRetryPublish {
			t.Errorf("%q não virou tentar de novo", s)
		}
	}
	for _, s := range []string{"Quero subir um anúncio de formulário", "subir o orçamento", "conectar conta", "agora", "tenta", "de novo",
		"quanto custa subir", "ativar"} {
		if cmd, _ := parseCampaignCommand(s); cmd == cmdRetryPublish {
			t.Errorf("%q virou tentar de novo", s)
		}
	}
}

func TestParseCampaignCommandActivateWording(t *testing.T) {
	for in, want := range map[string]string{"ativa aí": "", "Ativa ai!": "", "ok pode ativar agora": "", "ativar campanha um": "campanha 1",
		"Campanha um, ativar": "campanha 1", "ativa a 3": "3", "ativar a campanha dois por favor": "2", "ATIVAR Campanha 3": "campanha 3"} {
		if cmd, arg := parseCampaignCommand(in); cmd != cmdActivate || arg != want {
			t.Errorf("%q = %v %q, quer ativar %q", in, cmd, arg, want)
		}
	}
	if cmd, _ := parseCampaignCommand("pausa aí"); cmd != cmdPause {
		t.Error("pausa aí não pausou")
	}
}
