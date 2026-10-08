//go:build !unix

package application

func pipelineDesignOwnerDead(int) bool { return false }
