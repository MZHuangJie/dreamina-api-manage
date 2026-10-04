package store

import (
	"encoding/json"
	"errors"
	"strings"

	"dreamina-manager/internal/crypto"
)

// AccountPatch 是账号的部分更新。nil 表示该字段不改。
type AccountPatch struct {
	Name         *string
	Remark       *string
	Tags         *[]string
	Credential   *string
	Enabled      *bool
	ProxyURL     *string
	ProxyEnabled *bool
	StoreIDC     *string
	StoreCountry *string
}

// UpdateAccount 局部更新账号。更换凭据时会把旧的探活状态重置。
func (d *DB) UpdateAccount(id string, patch AccountPatch) (*Account, error) {
	current, err := d.GetAccount(id)
	if err != nil {
		return nil, err
	}

	sets := []string{"updated_at = ?"}
	args := []any{NowISO()}

	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			return nil, errors.New("账号名称不能为空")
		}
		sets = append(sets, "name = ?")
		args = append(args, name)
	}
	if patch.Remark != nil {
		sets = append(sets, "remark = ?")
		args = append(args, *patch.Remark)
	}
	if patch.Tags != nil {
		raw, _ := json.Marshal(*patch.Tags)
		sets = append(sets, "tags = ?")
		args = append(args, string(raw))
	}
	if patch.Enabled != nil {
		sets = append(sets, "enabled = ?")
		args = append(args, boolInt(*patch.Enabled))
	}
	if patch.ProxyURL != nil {
		sets = append(sets, "proxy_url = ?")
		args = append(args, strings.TrimSpace(*patch.ProxyURL))
	}
	if patch.ProxyEnabled != nil {
		sets = append(sets, "proxy_enabled = ?")
		args = append(args, boolInt(*patch.ProxyEnabled))
	}
	if patch.StoreIDC != nil {
		sets = append(sets, "store_idc = ?")
		args = append(args, *patch.StoreIDC)
	}
	if patch.StoreCountry != nil {
		sets = append(sets, "store_country = ?")
		args = append(args, *patch.StoreCountry)
	}

	if patch.Credential != nil {
		if d.key == nil {
			return nil, errors.New("数据库未配置加密密钥，无法更新凭据")
		}
		cipherText, err := d.key.Encrypt(*patch.Credential)
		if err != nil {
			return nil, err
		}
		sets = append(sets, "credential_hash = ?", "credential_ciphertext = ?")
		args = append(args, crypto.Hash(*patch.Credential), cipherText)
	}

	args = append(args, id)
	if _, err := d.sql.Exec("UPDATE accounts SET "+joinComma(sets)+" WHERE id = ?", args...); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrDuplicate
		}
		return nil, err
	}

	// 凭据变了，旧的状态快照立即失效
	if patch.Credential != nil {
		if _, err := d.sql.Exec(`UPDATE accounts SET health = 'unknown', status_error = NULL, 
			status_checked_at = NULL, failure_count = 0, cooldown_until = NULL WHERE id = ?`, id); err != nil {
			return nil, err
		}
	}
	// 停用就不能继续当「当前账号」
	if patch.Enabled != nil && !*patch.Enabled && current.IsActive {
		if _, err := d.sql.Exec("UPDATE accounts SET is_active = 0 WHERE id = ?", id); err != nil {
			return nil, err
		}
	}
	return d.GetAccount(id)
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
