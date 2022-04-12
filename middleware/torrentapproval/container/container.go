package container

import (
	"errors"
	"sync"

	"github.com/sot-tech/mochi/bittorrent"
	"github.com/sot-tech/mochi/storage"
)

// DefaultStorageCtxName default ctx name if value from configuration is not set
const DefaultStorageCtxName = "MW_APPROVAL"

// Builder function that creates and configures specific container
type Builder func([]byte, storage.Storage) (Container, error)

var (
	buildersMU sync.Mutex
	builders   = make(map[string]Builder)

	// ErrContainerDoesNotExist holds error about nonexistent container
	ErrContainerDoesNotExist = errors.New("torrent hash container with that name does not exist")
)

// Register used to register specific Builder in registry
func Register(n string, c Builder) {
	if len(n) == 0 {
		panic("middleware: could not register a Container with an empty name")
	}
	if c == nil {
		panic("middleware: could not register a Container with nil builder constructor")
	}

	buildersMU.Lock()
	defer buildersMU.Unlock()
	builders[n] = c
}

// Container holds InfoHash and checks if value approved or not
type Container interface {
	Approved(bittorrent.InfoHash) bool
}

// GetContainer creates Container by its name and provided confBytes
func GetContainer(name string, confBytes []byte, storage storage.Storage) (Container, error) {
	buildersMU.Lock()
	defer buildersMU.Unlock()
	var err error
	var cn Container
	if builder, exist := builders[name]; !exist {
		err = ErrContainerDoesNotExist
	} else {
		cn, err = builder(confBytes, storage)
	}
	return cn, err
}