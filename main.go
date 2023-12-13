package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
)

func main() {
	flag.Parse()
	if len(flag.Args()) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: rgdel <subscription_id> <resource_group_name_pattern>")
		os.Exit(1)
	}
	subscription := flag.Args()[0]
	rgPattern := flag.Args()[1]
	query := fmt.Sprintf("[?contains(name,'%s')]", rgPattern)

	cmd := exec.Command("az", "group", "list", "--query", query, "--subscription", subscription)
	fmt.Println(cmd.String())

	rgList, err := cmd.Output()
	if err != nil {
		fmt.Println(err)
		log.Fatal(err)
	}

	var rgListJSON []map[string]interface{}
	err = json.Unmarshal(rgList, &rgListJSON)
	if err != nil {
		fmt.Println(err)
		return
	}

	if len(rgListJSON) == 0 {
		fmt.Println("no resource groups found")
		return
	}

	var rgNames []string
	for _, rg := range rgListJSON {
		rgNames = append(rgNames, rg["name"].(string))
		fmt.Println(rg["name"].(string))
	}

	if !askForConfirm("the above resource groups will be deleted, continue? (y/n)") {
		return
	}

	var wg sync.WaitGroup
	for _, rg := range rgNames {
		wg.Add(1)

		r := rg
		go func() {
			defer wg.Done()
			groupDelWorker(r)
		}()
	}
	wg.Wait()
}

func groupDelWorker(rgName string) {
	groupUnlock(rgName)
	diskAccessRevoke(rgName)
	RecoveryVaultSoftDeleteRevoke(rgName)
	delCmd := exec.Command("az", "group", "delete", "--resource-group", rgName, "--yes")
	fmt.Println(delCmd.String())
	delCmd.Run()
	fmt.Printf("%s del done\n", rgName)
}

func groupUnlock(rgName string) {
	lockList, err := exec.Command("az", "group", "lock", "list", "--resource-group", rgName).Output()
	if err != nil {
		fmt.Println(err)
	}
	var lockJson []map[string]interface{}
	json.Unmarshal(lockList, &lockJson)
	for _, lock := range lockJson {
		lockId := lock["id"].(string)
		if lock["name"] != "ASR-Lock" {
			fmt.Println("unable to unlock", rgName, "lock id", lockId)
		}
		cmd := exec.Command("az", "lock", "delete", "--ids", lockId)
		fmt.Println(cmd.String())
		cmd.Run()
	}
}

func diskAccessRevoke(rgName string) {
	cmd := exec.Command("az", "disk", "list", "--resource-group", rgName, "--query", "[?contains(diskState,'ActiveSAS')]")
	fmt.Println(cmd.String())
	diskList, err := cmd.Output()
	if err != nil {
		fmt.Println(err)
	}

	var diskListJson []map[string]interface{}
	json.Unmarshal(diskList, &diskListJson)
	for _, disk := range diskListJson {
		diskId := disk["id"].(string)
		revokeCmd := exec.Command("az", "disk", "revoke-access", "--ids", diskId)
		fmt.Println(revokeCmd.String())
		revokeCmd.Run()
	}
}

// disable the soft delete feature and recover soft deleted items
// then they will be able to be deleted
func RecoveryVaultSoftDeleteRevoke(rgName string) {
	cmd := exec.Command("az", "backup", "vault", "list", "--resource-group", rgName)
	fmt.Println(cmd.String())
	vaultList, err := cmd.Output()
	if err != nil {
		fmt.Println(err)
	}

	var vaultListJson []map[string]interface{}
	json.Unmarshal(vaultList, &vaultListJson)
	for _, vault := range vaultListJson {
		vaultId := vault["id"].(string)
		vaultName := vault["name"].(string)
		softDeleteFeatureRevoke(vaultId)
		deleteSoftdeltedItems(rgName, vaultName)
	}
}

func softDeleteFeatureRevoke(vaultId string) {
	cmd := exec.Command("az", "backup", "vault", "backup-properties", "set", "--ids", vaultId, "--soft-delete-feature-state", "disable")
	fmt.Println(cmd.String())
	cmd.Run()
}

func deleteSoftdeltedItems(rgName string, vaultName string) {
	cmd := exec.Command("az", "backup", "item", "list", "--resource-group", rgName, "--vault-name", vaultName)
	fmt.Println(cmd.String())
	itemsList, err := cmd.Output()
	if err != nil {
		fmt.Println(err)
	}

	var itemsListJson []map[string]interface{}
	json.Unmarshal(itemsList, &itemsListJson)
	for _, item := range itemsListJson {
		id := item["id"].(string)
		recoverCmd := exec.Command("az", "backup", "protection", "undelete", "--ids", id)
		fmt.Println(recoverCmd.String())
		recoverCmd.Run()

		deleteCmd := exec.Command("az", "backup", "protection", "disable", "--ids", id, "--yes")
		fmt.Println(deleteCmd.String())
		deleteCmd.Run()
	}
}

func askForConfirm(msg string) bool {
	fmt.Println(msg)

	var response string
	_, err := fmt.Scanln(&response)
	if err != nil {
		log.Fatal(err)
	}

	switch strings.ToLower(response) {
	case "y":
		return true
	default:
		return false
	}
}
