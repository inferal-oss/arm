package providerexec

import (
	"encoding/json"
	"testing"
)

func TestReviewNestedAncestorRequiredFields(t *testing.T) {
	base,err:=json.Marshal(imagePacket{Ancestors:[]directoryWitness{{Mode:0700,ACLSHA256:"digest"}}})
	if err != nil {t.Fatal(err)}
	var valid imagePacket
	if err=strictJSON(base,&valid);err != nil {t.Fatal("complete typed wire control",err)}
	for _,kind:=range []string{"missing-mode","null-mode","missing-identity","null-element","missing-owner","null-owner","alias-owner"} {
		t.Run(kind,func(t *testing.T){
			var fields map[string]any
			if err=json.Unmarshal(base,&fields);err != nil {t.Fatal(err)}
			ancestors:=fields["ancestors"].([]any)
			item:=ancestors[0].(map[string]any)
			switch kind {
			case "missing-mode":delete(item,"mode")
			case "null-mode":item["mode"]=nil
			case "missing-identity":delete(item,"identity")
			case "null-element":ancestors[0]=nil
			case "missing-owner":delete(item["identity"].(map[string]any),"owner")
			case "null-owner":item["identity"].(map[string]any)["owner"]=nil
			case "alias-owner":id:=item["identity"].(map[string]any);id["OWNER"]=id["owner"];delete(id,"owner")
			}
			body,err:=json.Marshal(fields)
			if err != nil {t.Fatal(err)}
			var decoded imagePacket
			if err=strictJSON(body,&decoded);err == nil {t.Fatalf("nested ancestor wire accepted %s; zero coercion/case aliases must not erase required evidence",kind)}
		})
	}
}
