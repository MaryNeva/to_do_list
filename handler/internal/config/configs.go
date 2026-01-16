package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Handler Handler `yaml:"handler,omitempty"`
}

type Handler struct {
	App           App           `yaml:"app"`
	Repository    Repository    `yaml:"repository"`
	Authorization Authorization `yaml:"authorization"`
	Security      Secutity      `yaml:"security"`
	HealthSecret  string        `yaml:"healthSecret"`
}

type App struct {
	Address string `yaml:"address"`
}

type Authorization struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type Repository struct {
	Host     string `yaml:"host"`
	Port     uint16 `yaml:"port"`
	DbName   string `yaml:"db_name"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}

type Secutity struct {
	JWT JWT `json:"jwt"`
}

type JWT struct {
	Secret    string `yaml:"secret"`
	ExpiresIn string `yaml:"expiresIn"`
}

func LoadConfig[T any](path string) (T, error) {
	var cfg T

	v := reflect.ValueOf(&cfg).Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("yaml")
		name := strings.Split(tag, ",")[0]
		if name == "" {
			continue
		}

		filePath := filepath.Join(path, name+".yaml")

		data, err := os.ReadFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return cfg, err
		}

		fieldPtr := v.Field(i).Addr().Interface()

		if err = yaml.Unmarshal(data, fieldPtr); err != nil {
			return cfg, err
		}
	}

	return cfg, nil
}

func (x *Repository) GetPath() string {
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", x.User, x.Password, net.JoinHostPort(x.Host, strconv.Itoa(int(x.Port))), x.DbName)
}
