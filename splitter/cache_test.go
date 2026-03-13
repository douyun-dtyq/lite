package splitter

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	mathRand "math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	git "github.com/libgit2/git2go/v34"
	"github.com/stretchr/testify/assert"
)

func TestCache(t *testing.T) {

	var err error
	pwd, err := os.Getwd()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	defer os.Chdir(pwd)

	tempDir := t.TempDir()
	defer os.RemoveAll(tempDir)
	os.Chdir(tempDir)

	_, bCommitHashes, headCommitHash := makeTestRepo(t, tempDir)

	// test config
	config := &Config{
		Path:       filepath.Join(tempDir, "test-repo"),
		GitVersion: "latest",
		Git:        3,
		Origin:     "HEAD",
		Target:     "HEAD",
		Prefixes: []*Prefix{
			{
				From: "dira",
				To:   "/path/to/dira",
			},
		},
	}

	// test cache
	cache, err := newCache("b", config)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	defer cache.close()

	headCommitHashBytes, err := hex.DecodeString(headCommitHash)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	testOidHead := git.NewOidFromBytes(headCommitHashBytes)
	fmt.Println(testOidHead.String())
	oid2CommitHashBytes, err := hex.DecodeString(bCommitHashes[0])
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	testOid2 := git.NewOidFromBytes(oid2CommitHashBytes)

	rng := mathRand.ChaCha8{}
	seed := [32]byte{}
	rand.Read(seed[:])
	rng.Seed(seed)

	// test set/get head
	t.Run("GetSetHead", func(t *testing.T) {
		oidGot := cache.getHead()
		assert.Nil(t, oidGot)

		cache.setHead(testOid2)
		oidGot = cache.getHead()
		if !assert.NotNil(t, oidGot) {
			t.FailNow()
		}
		assert.Equal(t, testOid2.String(), oidGot.String())
	})

	t.Run("GetSet", func(t *testing.T) {
		oidGot := cache.get(testOid2)
		assert.Nil(t, oidGot)

		cache.set(testOid2, testOidHead)
		oidGot = cache.get(testOid2)
		if !assert.NotNil(t, oidGot) {
			t.FailNow()
		}
		assert.Equal(t, testOidHead.String(), oidGot.String())

		const testOidCount = 10
		testOidKeys := make([][]byte, testOidCount*20)
		testOidValues := make([][]byte, testOidCount*20)
		for i := 0; i < testOidCount; i++ {
			buf := make([]byte, 40)
			rng.Read(buf)
			testOidKeys[i] = buf[0:20]
			testOidValues[i] = buf[20:40]

			oidKey := git.NewOidFromBytes(testOidKeys[i])
			oidValue := git.NewOidFromBytes(testOidValues[i])
			cache.set(oidKey, oidValue)
			oidGot := cache.get(oidKey)
			assert.Equal(t, oidValue.String(), oidGot.String())
		}

		testGets := make([]*git.Oid, testOidCount)
		for i := 0; i < testOidCount; i++ {
			testGets[i] = git.NewOidFromBytes(testOidKeys[i])
		}
		oidsGot := cache.gets(testGets)
		for i := 0; i < testOidCount; i++ {
			assert.Equal(t, git.NewOidFromBytes(testOidValues[i]).String(), oidsGot[i].String())
		}
	})
}
func TestCacheFlush(t *testing.T) {
	var err error
	pwd, err := os.Getwd()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	defer os.Chdir(pwd)

	tempDir := t.TempDir()
	defer os.RemoveAll(tempDir)
	os.Chdir(tempDir)

	_, _, headCommitHash := makeTestRepo(t, tempDir)

	config := &Config{
		Path:       filepath.Join(tempDir, "test-repo"),
		GitVersion: "latest",
		Git:        3,
		Origin:     "HEAD",
		Target:     "HEAD",
	}
	cache, err := newCache("b", config)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	defer cache.close()

	headCommitHashBytes, err := hex.DecodeString(headCommitHash)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	testOidHead := git.NewOidFromBytes(headCommitHashBytes)

	rng := mathRand.ChaCha8{}
	seed := [32]byte{}
	rand.Read(seed[:])
	rng.Seed(seed)

	// set head
	cache.setHead(testOidHead)
	// set some commits
	const testOidCount = 10
	testOidKeys := make([][]byte, testOidCount)
	testOidValues := make([][]byte, testOidCount)
	for i := 0; i < testOidCount; i++ {
		buf := make([]byte, 40)
		rng.Read(buf)
		testOidKeys[i] = buf[0:20]
		testOidValues[i] = buf[20:40]

		oidKey := git.NewOidFromBytes(testOidKeys[i])
		oidValue := git.NewOidFromBytes(testOidValues[i])
		cache.set(oidKey, oidValue)
	}

	// flush it
	err = cache.flush()
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	cache.close()

	// create new cache
	newCache, err := newCache("b", config)
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	defer newCache.close()

	// check if the head is set
	headGot := newCache.getHead()
	assert.Equal(t, testOidHead.String(), headGot.String())

	// check if the commits are set
	for i := 0; i < testOidCount; i++ {
		oidKey := git.NewOidFromBytes(testOidKeys[i])
		oidValue := git.NewOidFromBytes(testOidValues[i])
		oidGot := newCache.get(oidKey)
		assert.Equal(t, oidValue.String(), oidGot.String())
	}
}
