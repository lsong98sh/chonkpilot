package persist

import (
	"testing"

	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
)

// TestDataReqSubjectsIncludesKnowledgeImport 回归 KB-010：知识库导入/“其他类型新建”必须有
// 实际订阅，否则 handler 存在但消息永不送达（2026-11-04 缺口）。
func TestDataReqSubjectsIncludesKnowledgeImport(t *testing.T) {
	for _, subj := range dataReqSubjects {
		if subj == msgkeys.TopicDataKnowledgeImport {
			return
		}
	}
	t.Fatalf("dataReqSubjects 缺少 %s（知识库导入运行时不可达）", msgkeys.TopicDataKnowledgeImport)
}