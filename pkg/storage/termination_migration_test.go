package storage

import (
 "testing"
)

func TestTerminationModeSQLiteLegacyMigration(t *testing.T) {
 path:=t.TempDir()+"/legacy.db"
 s,err:=NewSQLiteStore(path);if err!=nil {t.Fatal(err)}
 if err:=s.SaveScenario(&Scenario{ID:"legacy",Name:"Legacy",Duration:"1m"},nil);err!=nil {t.Fatal(err)}
 for _,table:=range []string{"scenarios","tests"} {if _,err:=s.db.Exec("ALTER TABLE "+table+" DROP COLUMN termination_mode");err!=nil {t.Fatal(err)}}
 if err:=s.Close();err!=nil {t.Fatal(err)}
 s,err=NewSQLiteStore(path);if err!=nil {t.Fatal(err)};defer s.Close()
 legacy,_,err:=s.GetScenario("legacy");if err!=nil {t.Fatal(err)}
 if legacy.TerminationMode!="duration" || legacy.Duration!="1m" {t.Fatalf("legacy=%+v",legacy)}
 if err:=s.SaveScenario(&Scenario{ID:"finite",Name:"Finite",TerminationMode:"iterations"},nil);err!=nil {t.Fatal(err)}
 finite,_,err:=s.GetScenario("finite");if err!=nil || finite.TerminationMode!="iterations" || finite.Duration!="" {t.Fatalf("finite=%+v err=%v",finite,err)}
}
