<template>
  <div class="wizard-root">
    <!-- 顶部工具条：标题 / 工作目录 / 步骤指示（对话框标题由 DialogShell 提供） -->
    <div class="wz-toolbar">
      <span class="wz-title">{{ $t('wizard.title') }}</span>
      <span class="wz-path" :title="workDir">{{ $t('wizard.workDir') }}: {{ workDir || '-' }}</span>
      <span class="wz-step">{{ $t('wizard.step', { n: step }) }} · {{ stepLabel }}</span>
    </div>

    <div class="wz-main">
      <!-- 左：步骤导航（可点击） -->
      <nav class="wz-rail">
        <ul class="wz-rail-list">
          <li
            v-for="s in wizardSteps"
            :key="s.n"
            class="wz-rail-item"
            :class="{ on: step === s.n, done: step > s.n }"
            @click="goStep(s.n)"
          >
            <span class="wz-rail-n">{{ s.n }}</span>
            <span class="wz-rail-label">{{ s.label }}</span>
          </li>
        </ul>
      </nav>

      <!-- 中：内容区（可滚动） -->
      <div class="wz-content">
        <!-- ── Step1 项目信息 ── -->
        <section v-show="step === 1" class="wz-panel">
          <div class="wz-banner">{{ $t('wizard.info.banner') }}</div>
          <fieldset class="wz-fieldset">
            <legend>
              {{ $t('wizard.info.legend') }}
              <span class="wz-legend-note">{{ $t('wizard.info.legendNote') }}</span>
            </legend>

            <div class="wz-field">
              <label class="wz-label">
                {{ $t('wizard.info.goal') }} <span class="wz-req">*</span>
                <Tooltip :content="$t('wizard.info.goalTip')"><span class="wz-tip">?</span></Tooltip>
              </label>
              <Textarea v-model="info.goal" :rows="2" />
            </div>
            <div class="wz-field">
              <label class="wz-label">
                {{ $t('wizard.info.summary') }}
                <Tooltip :content="$t('wizard.info.summaryTip')"><span class="wz-tip">?</span></Tooltip>
              </label>
              <Textarea v-model="info.summary" :rows="3" />
            </div>
            <div class="wz-field">
              <label class="wz-label">
                {{ $t('wizard.info.budget') }}
                <Tooltip :content="$t('wizard.info.budgetTip')"><span class="wz-tip">?</span></Tooltip>
              </label>
              <Input v-model="info.budget" />
            </div>
            <div class="wz-field">
              <label class="wz-label">
                {{ $t('wizard.info.deploy') }}
                <Tooltip :content="$t('wizard.info.deployTip')"><span class="wz-tip">?</span></Tooltip>
              </label>
              <Input v-model="info.deploy" />
            </div>
            <div class="wz-field">
              <label class="wz-label">
                {{ $t('wizard.info.period') }}
                <Tooltip :content="$t('wizard.info.periodTip')"><span class="wz-tip">?</span></Tooltip>
              </label>
              <Input v-model="info.period" />
            </div>
            <div class="wz-field">
              <label class="wz-label">
                {{ $t('wizard.info.background') }}
                <Tooltip :content="$t('wizard.info.backgroundTip')"><span class="wz-tip">?</span></Tooltip>
              </label>
              <Textarea v-model="info.background" :rows="2" />
            </div>
            <div class="wz-field">
              <label class="wz-label">
                {{ $t('wizard.info.requirement') }}
                <Tooltip :content="$t('wizard.info.requirementTip')"><span class="wz-tip">?</span></Tooltip>
              </label>
              <Textarea v-model="info.requirement" :rows="2" />
            </div>
          </fieldset>
        </section>

        <!-- ── Step2 模式选择 ── -->
        <section v-show="step === 2" class="wz-panel">
          <div class="wz-banner">{{ $t('wizard.mode.banner') }}</div>
          <fieldset class="wz-fieldset">
            <legend>{{ $t('wizard.mode.legend') }}</legend>
            <div class="wz-cards">
              <label class="wz-card" :class="{ on: mode === 'A' }">
                <input type="radio" name="wz-mode" :checked="mode === 'A'" @change="setMode('A')" />
                <span class="wz-card-txt">
                  <span class="wz-card-title">{{ $t('wizard.mode.newBuild') }}</span>
                  <span class="wz-card-desc">{{ $t('wizard.mode.newBuildDesc') }}</span>
                </span>
              </label>
              <label class="wz-card" :class="{ on: mode === 'B' }">
                <input type="radio" name="wz-mode" :checked="mode === 'B'" @change="setMode('B')" />
                <span class="wz-card-txt">
                  <span class="wz-card-title">{{ $t('wizard.mode.iterate') }}</span>
                  <span class="wz-card-desc">{{ $t('wizard.mode.iterateDesc') }}</span>
                </span>
              </label>
              <label class="wz-card" :class="{ on: mode === 'C' }">
                <input type="radio" name="wz-mode" :checked="mode === 'C'" @change="setMode('C')" />
                <span class="wz-card-txt">
                  <span class="wz-card-title">{{ $t('wizard.mode.refactor') }}</span>
                  <span class="wz-card-desc">{{ $t('wizard.mode.refactorDesc') }}</span>
                </span>
              </label>
              <label class="wz-card" :class="{ on: mode === 'other' }">
                <input type="radio" name="wz-mode" :checked="mode === 'other'" @change="setMode('other')" />
                <span class="wz-card-txt">
                  <span class="wz-card-title">{{ $t('wizard.mode.other') }}</span>
                  <span class="wz-card-desc">{{ $t('wizard.mode.otherDesc') }}</span>
                </span>
              </label>
            </div>
            <Input
              v-if="mode === 'other'"
              v-model="modeOther"
              class="wz-custom"
              :placeholder="$t('wizard.mode.otherPlaceholder')"
            />
          </fieldset>
          <div class="wz-quick">
            <Button size="small" :loading="generating" @click="generateDefault">{{ $t('wizard.common.default') }}</Button>
            <span class="wz-hint">{{ $t('wizard.mode.defaultHint') }}</span>
          </div>
        </section>

        <!-- ── Step3 技术栈与架构 ── -->
        <section v-show="step === 3" class="wz-panel">
          <div class="wz-banner">{{ stackBanner }}</div>

          <!-- 模式 A：新构建 -->
          <template v-if="mode === 'A'">
            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.ptype.legend') }}<span class="wz-legend-note">{{ $t('wizard.stack.ptype.note') }}</span></legend>
              <div class="wz-cards">
                <label v-for="o in withOther(PTYPE_OPTS)" :key="o.value" class="wz-card" :class="{ on: sel.ptype === o.value }">
                  <input type="radio" name="wz-ptype" :checked="sel.ptype === o.value" @change="sel.ptype = o.value" />
                  <span class="wz-card-txt">
                    <span class="wz-card-title">{{ $t(o.label) }}</span>
                    <span v-if="o.desc" class="wz-card-desc">{{ $t(o.desc) }}</span>
                    <span v-if="o.team" class="wz-card-team">{{ $t(o.team) }}</span>
                  </span>
                </label>
              </div>
              <Input v-if="sel.ptype === OTHER" v-model="custom.ptype" class="wz-custom" :placeholder="$t('wizard.common.otherPlaceholder')" />
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.platform.legend') }}<span class="wz-legend-note">{{ $t('wizard.stack.platform.note') }}</span></legend>
              <div class="wz-chips">
                <label v-for="o in withOther(PLATFORM_OPTS)" :key="o.value" class="wz-chip" :class="{ on: sel.platforms.includes(o.value) }">
                  <input type="checkbox" :checked="sel.platforms.includes(o.value)" @change="onPlatformToggle(o)" />
                  {{ $t(o.label) }}
                </label>
              </div>
              <Input v-if="sel.platforms.includes(OTHER)" v-model="custom.platforms" class="wz-custom" :placeholder="$t('wizard.common.otherPlaceholder')" />
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.feLang.legend') }}<span class="wz-legend-note">{{ $t('wizard.stack.feLang.note') }}</span></legend>
              <div class="wz-chips">
                <label v-for="o in withOther(FE_LANG_OPTS)" :key="o.value" class="wz-chip" :class="{ on: sel.feLangs.includes(o.value) }">
                  <input type="checkbox" :checked="sel.feLangs.includes(o.value)" @change="toggleArr(sel.feLangs, o.value)" />
                  {{ $t(o.label) }}
                </label>
              </div>
              <Input v-if="sel.feLangs.includes(OTHER)" v-model="custom.feLang" class="wz-custom" :placeholder="$t('wizard.common.otherPlaceholder')" />
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.feFw.legend') }}</legend>
              <div class="wz-chips">
                <label v-for="o in withOther(FE_FW_OPTS)" :key="o.value" class="wz-chip is-radio" :class="{ on: sel.feFw === o.value }">
                  <input type="radio" name="wz-fefw" :checked="sel.feFw === o.value" @change="sel.feFw = o.value" />
                  {{ $t(o.label) }}
                </label>
              </div>
              <Input v-if="sel.feFw === OTHER" v-model="custom.feFw" class="wz-custom" :placeholder="$t('wizard.common.otherPlaceholder')" />
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.feUi.legend') }}</legend>
              <div class="wz-chips">
                <label v-for="o in withOther(FE_UI_OPTS)" :key="o.value" class="wz-chip is-radio" :class="{ on: sel.feUi === o.value }">
                  <input type="radio" name="wz-feui" :checked="sel.feUi === o.value" @change="sel.feUi = o.value" />
                  {{ $t(o.label) }}
                </label>
              </div>
              <Input v-if="sel.feUi === OTHER" v-model="custom.feUi" class="wz-custom" :placeholder="$t('wizard.common.otherPlaceholder')" />
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.beLang.legend') }}<span class="wz-legend-note">{{ $t('wizard.stack.beLang.note') }}</span></legend>
              <div class="wz-chips">
                <label v-for="o in withOther(BE_LANG_OPTS)" :key="o.value" class="wz-chip" :class="{ on: sel.beLangs.includes(o.value) }">
                  <input type="checkbox" :checked="sel.beLangs.includes(o.value)" @change="toggleArr(sel.beLangs, o.value)" />
                  {{ $t(o.label) }}
                </label>
              </div>
              <Input v-if="sel.beLangs.includes(OTHER)" v-model="custom.beLang" class="wz-custom" :placeholder="$t('wizard.common.otherPlaceholder')" />
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.beFw.legend') }}</legend>
              <div class="wz-chips">
                <label v-for="o in withOther(BE_FW_OPTS)" :key="o.value" class="wz-chip is-radio" :class="{ on: sel.beFw === o.value }">
                  <input type="radio" name="wz-befw" :checked="sel.beFw === o.value" @change="sel.beFw = o.value" />
                  {{ $t(o.label) }}
                </label>
              </div>
              <Input v-if="sel.beFw === OTHER" v-model="custom.beFw" class="wz-custom" :placeholder="$t('wizard.common.otherPlaceholder')" />
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.db.legend') }}</legend>
              <div class="wz-chips">
                <label v-for="o in withOther(DB_OPTS)" :key="o.value" class="wz-chip is-radio" :class="{ on: sel.db === o.value }">
                  <input type="radio" name="wz-db" :checked="sel.db === o.value" @change="sel.db = o.value" />
                  {{ $t(o.label) }}
                </label>
              </div>
              <Input v-if="sel.db === OTHER" v-model="custom.db" class="wz-custom" :placeholder="$t('wizard.common.otherPlaceholder')" />
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.orm.legend') }}</legend>
              <div class="wz-chips">
                <label v-for="o in withOther(ORM_OPTS)" :key="o.value" class="wz-chip is-radio" :class="{ on: sel.orm === o.value }">
                  <input type="radio" name="wz-orm" :checked="sel.orm === o.value" @change="sel.orm = o.value" />
                  {{ $t(o.label) }}
                </label>
              </div>
              <Input v-if="sel.orm === OTHER" v-model="custom.orm" class="wz-custom" :placeholder="$t('wizard.common.otherPlaceholder')" />
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.arch.legend') }}<span class="wz-legend-note">{{ $t('wizard.stack.arch.note') }}</span></legend>
              <div class="wz-cards">
                <label v-for="o in withOther(ARCH_OPTS)" :key="o.value" class="wz-card" :class="{ on: sel.arch === o.value }">
                  <input type="radio" name="wz-arch" :checked="sel.arch === o.value" @change="sel.arch = o.value" />
                  <span class="wz-card-txt">
                    <span class="wz-card-title">{{ $t(o.label) }}</span>
                    <span v-if="o.desc" class="wz-card-desc">{{ $t(o.desc) }}</span>
                    <span v-if="o.team" class="wz-card-team">{{ $t(o.team) }}</span>
                  </span>
                </label>
              </div>
              <Input v-if="sel.arch === OTHER" v-model="custom.arch" class="wz-custom" :placeholder="$t('wizard.common.otherPlaceholder')" />
            </fieldset>
          </template>

          <!-- 模式 B：迭代 -->
          <template v-else-if="mode === 'B'">
            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.b.accessLegend') }}</legend>
              <div class="wz-cards">
                <label v-for="o in withOther(B_ACCESS_OPTS)" :key="o.value" class="wz-card" :class="{ on: sel.bAccess === o.value }">
                  <input type="radio" name="wz-baccess" :checked="sel.bAccess === o.value" @change="sel.bAccess = o.value" />
                  <span class="wz-card-txt">
                    <span class="wz-card-title">{{ $t(o.label) }}</span>
                    <span v-if="o.desc" class="wz-card-desc">{{ $t(o.desc) }}</span>
                  </span>
                </label>
              </div>
              <Input v-if="sel.bAccess === OTHER" v-model="custom.bAccess" class="wz-custom" :placeholder="$t('wizard.common.otherPlaceholder')" />
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>
                {{ $t('wizard.stack.b.reconLegend') }}<span class="wz-legend-note">{{ $t('wizard.stack.b.reconNote') }}</span>
                <Button text size="mini" class="wz-inline-btn" :loading="probeLoading" @click="runProbe">{{ $t('wizard.common.rescan') }}</Button>
              </legend>
              <div class="wz-recon">{{ reconText }}</div>
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.b.impactLegend') }}</legend>
              <div class="wz-sub-label">{{ $t('wizard.stack.b.scopeLabel') }}</div>
              <div class="wz-chips">
                <label v-for="o in B_SCOPE_OPTS" :key="o.value" class="wz-chip is-radio" :class="{ on: sel.bScope === o.value }">
                  <input type="radio" name="wz-bscope" :checked="sel.bScope === o.value" @change="sel.bScope = o.value" />
                  {{ $t(o.label) }}
                </label>
              </div>
              <div class="wz-sub-label">{{ $t('wizard.stack.b.depLabel') }}</div>
              <div class="wz-chips">
                <label v-for="o in B_DEP_OPTS" :key="o.value" class="wz-chip is-radio" :class="{ on: sel.bDep === o.value }">
                  <input type="radio" name="wz-bdep" :checked="sel.bDep === o.value" @change="sel.bDep = o.value" />
                  {{ $t(o.label) }}
                </label>
              </div>
              <div class="wz-sub-label">{{ $t('wizard.stack.b.testLabel') }}</div>
              <div class="wz-chips">
                <label v-for="o in B_TEST_OPTS" :key="o.value" class="wz-chip is-radio" :class="{ on: sel.bTest === o.value }">
                  <input type="radio" name="wz-btest" :checked="sel.bTest === o.value" @change="sel.bTest = o.value" />
                  {{ $t(o.label) }}
                </label>
              </div>
              <div class="wz-sub-label">{{ $t('wizard.stack.b.branchLabel') }}</div>
              <div class="wz-chips">
                <label v-for="o in B_BRANCH_OPTS" :key="o.value" class="wz-chip is-radio" :class="{ on: sel.bBranch === o.value }">
                  <input type="radio" name="wz-bbranch" :checked="sel.bBranch === o.value" @change="sel.bBranch = o.value" />
                  {{ $t(o.label) }}
                </label>
              </div>
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.b.rhythmLegend') }}</legend>
              <div class="wz-chips">
                <label v-for="o in B_RHYTHM_OPTS" :key="o.value" class="wz-chip is-radio" :class="{ on: sel.bRhythm === o.value }">
                  <input type="radio" name="wz-brhythm" :checked="sel.bRhythm === o.value" @change="sel.bRhythm = o.value" />
                  {{ $t(o.label) }}
                </label>
              </div>
            </fieldset>
          </template>

          <!-- 模式 C：重构 -->
          <template v-else-if="mode === 'C'">
            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.c.targetLegend') }}</legend>
              <div class="wz-cards">
                <label v-for="o in withOther(C_TARGET_OPTS)" :key="o.value" class="wz-card" :class="{ on: sel.cTarget === o.value }">
                  <input type="radio" name="wz-ctarget" :checked="sel.cTarget === o.value" @change="sel.cTarget = o.value" />
                  <span class="wz-card-txt">
                    <span class="wz-card-title">{{ $t(o.label) }}</span>
                    <span v-if="o.desc" class="wz-card-desc">{{ $t(o.desc) }}</span>
                  </span>
                </label>
              </div>
              <Input v-if="sel.cTarget === OTHER" v-model="custom.cTarget" class="wz-custom" :placeholder="$t('wizard.common.otherPlaceholder')" />
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.c.techLegend') }}<span class="wz-legend-note">{{ $t('wizard.stack.c.techNote') }}</span></legend>
              <div class="wz-recon">{{ $t('wizard.stack.c.techCurrent') }}: {{ currentStackText }}</div>
              <template v-if="sel.cTarget === 'stack'">
                <div class="wz-sub-label">{{ $t('wizard.stack.c.techTargetLabel') }}</div>
                <div class="wz-chips">
                  <label v-for="o in withOther(C_TLANG_OPTS)" :key="o.value" class="wz-chip is-radio" :class="{ on: sel.cTlang === o.value }">
                    <input type="radio" name="wz-ctlang" :checked="sel.cTlang === o.value" @change="sel.cTlang = o.value" />
                    {{ $t(o.label) }}
                  </label>
                </div>
                <Input v-if="sel.cTlang === OTHER" v-model="custom.cTlang" class="wz-custom" :placeholder="$t('wizard.stack.c.techTargetPlaceholder')" />
              </template>
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.c.keepLegend') }}<span class="wz-legend-note">{{ $t('wizard.stack.c.keepNote') }}</span></legend>
              <div class="wz-cards">
                <label v-for="o in C_KEEP_OPTS" :key="o.value" class="wz-card" :class="{ on: sel.cKeep === o.value }">
                  <input type="radio" name="wz-ckeep" :checked="sel.cKeep === o.value" @change="sel.cKeep = o.value" />
                  <span class="wz-card-txt">
                    <span class="wz-card-title">{{ $t(o.label) }}</span>
                    <span class="wz-card-desc">{{ $t(o.desc) }}</span>
                  </span>
                </label>
              </div>
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.c.scopeLegend') }}</legend>
              <div class="wz-sub-label">{{ $t('wizard.stack.c.rangeLabel') }}</div>
              <div class="wz-chips">
                <label v-for="o in C_RANGE_OPTS" :key="o.value" class="wz-chip is-radio" :class="{ on: sel.cRange === o.value }">
                  <input type="radio" name="wz-crange" :checked="sel.cRange === o.value" @change="sel.cRange = o.value" />
                  {{ $t(o.label) }}
                </label>
              </div>
              <div class="wz-sub-label">{{ $t('wizard.stack.c.verifyLabel') }}</div>
              <div class="wz-chips">
                <label v-for="o in C_VERIFY_OPTS" :key="o.value" class="wz-chip" :class="{ on: sel.cVerify.includes(o.value) }">
                  <input type="checkbox" :checked="sel.cVerify.includes(o.value)" @change="toggleArr(sel.cVerify, o.value)" />
                  {{ $t(o.label) }}
                </label>
              </div>
            </fieldset>

            <fieldset class="wz-fieldset">
              <legend>{{ $t('wizard.stack.c.rolesLegend') }}<span class="wz-legend-note">{{ $t('wizard.stack.c.rolesNote') }}</span></legend>
              <div class="wz-chips">
                <label v-for="o in C_ROLE_OPTS" :key="o.value" class="wz-chip" :class="{ on: sel.cRoles.includes(o.value) }">
                  <input type="checkbox" :checked="sel.cRoles.includes(o.value)" @change="toggleArr(sel.cRoles, o.value)" />
                  {{ $t(o.label) }}
                </label>
              </div>
            </fieldset>
          </template>

          <!-- 模式 其他：跳过模板 -->
          <template v-else>
            <div class="wz-placeholder">{{ $t('wizard.stack.otherModeHint') }}</div>
            <div class="wz-quick">
              <Button size="small" @click="closeDialog">{{ $t('wizard.stack.otherModeEnter') }}</Button>
            </div>
          </template>
        </section>

        <!-- ── Step4 摘要确认 ── -->
        <section v-show="step === 4" class="wz-panel">
          <div class="wz-banner">{{ $t('wizard.summary.banner') }}</div>
          <fieldset class="wz-fieldset">
            <legend>{{ $t('wizard.summary.legend') }}</legend>
            <div class="wz-field">
              <label class="wz-label">{{ $t('wizard.summary.summary') }}</label>
              <Textarea v-model="summaryEdits.summaryText" :rows="3" />
            </div>
            <div class="wz-row2">
              <div class="wz-field">
                <label class="wz-label">{{ $t('wizard.summary.arch') }}</label>
                <Input v-model="summaryEdits.archText" />
              </div>
              <div class="wz-field">
                <label class="wz-label">{{ $t('wizard.summary.lang') }}</label>
                <Input v-model="summaryEdits.langText" />
              </div>
            </div>
            <div class="wz-field">
              <label class="wz-label">{{ $t('wizard.summary.team') }}<span class="wz-legend-note">{{ $t('wizard.summary.teamNote') }}</span></label>
              <div class="wz-members">
                <span v-for="(m, idx) in team" :key="m.name + idx" class="wz-member">
                  {{ m.name }}<span v-if="m.isMain" class="wz-member-tag">main</span>
                  <button v-if="!m.isMain" type="button" class="wz-x" :title="$t('wizard.common.remove')" @click="removeTeamMember(idx)">✕</button>
                </span>
              </div>
              <Button text size="mini" @click="addTeamMember">{{ $t('wizard.common.addMember') }}</Button>
            </div>
          </fieldset>

          <fieldset class="wz-fieldset">
            <legend>{{ $t('wizard.summary.auxLegend') }}<span class="wz-legend-note">{{ $t('wizard.summary.auxNote') }}</span></legend>
            <div class="wz-aux">
              <label class="wz-aux-item"><Switch v-model="aux.memory" /><span>{{ $t('wizard.summary.auxMemory') }}</span></label>
              <label class="wz-aux-item"><Switch v-model="aux.userPref" /><span>{{ $t('wizard.summary.auxUserPref') }}</span></label>
              <label class="wz-aux-item"><Switch v-model="aux.codegraph" /><span>{{ $t('wizard.summary.auxCodegraph') }}</span></label>
              <label class="wz-aux-item"><Switch v-model="aux.vfts" /><span>{{ $t('wizard.summary.auxVfts') }}</span></label>
              <label class="wz-aux-item"><Switch v-model="aux.fileHistory" /><span>{{ $t('wizard.summary.auxFileHistory') }}</span></label>
              <label v-if="gitOffered" class="wz-aux-item"><Switch v-model="aux.initGit" /><span>{{ $t('wizard.summary.auxInitGit') }}</span></label>
            </div>
          </fieldset>
        </section>

        <!-- ── Step5 预览与生成 ── -->
        <section v-show="step === 5" class="wz-panel wz-panel-preview">
          <div class="wz-pv-toolbar">
            <Input v-model="sceneName" class="wz-scene-name" :placeholder="$t('wizard.preview.sceneName')" />
            <span class="wz-legend-note">{{ $t('wizard.preview.projectLevel') }}</span>
            <span class="wz-pv-grow" />
            <span class="wz-hint">{{ $t('wizard.preview.willWrite') }}</span>
          </div>

          <Tabs class="wz-pv-tabs" :tabs="previewTabs" v-model="previewTab">
            <!-- 场景 tab -->
            <template #scene>
              <div class="wz-pv-split">
                <div class="wz-agent-list">
                  <div class="wz-group-h">{{ $t('wizard.preview.mainAgent') }}</div>
                  <div
                    v-for="(a, idx) in mainAgents"
                    :key="'m' + idx"
                    class="wz-agent-item"
                    :class="{ on: selectedAgentIdx === idx }"
                    @click="selectedAgentIdx = idx"
                  >
                    <b>{{ a.name }}</b><span class="wz-member-tag">main</span>
                  </div>
                  <div class="wz-group-h">{{ $t('wizard.preview.subAgents', { n: subAgents.length }) }}</div>
                  <div
                    v-for="(a, idx) in subAgents"
                    :key="'s' + idx"
                    class="wz-agent-item"
                    :class="{ on: selectedAgentIdx === (idx + mainAgents.length) }"
                    @click="selectedAgentIdx = idx + mainAgents.length"
                  >
                    <b>{{ a.name }}</b>
                    <span v-if="a.roleTag" class="wz-member-tag">{{ a.roleTag }}</span>
                    <button type="button" class="wz-x" :title="$t('wizard.common.remove')" @click.stop="removeAgent(idx + mainAgents.length)">✕</button>
                  </div>
                  <Button text size="mini" @click="addSubAgent">{{ $t('wizard.common.addMember') }}</Button>
                  <p class="wz-hint">{{ $t('wizard.preview.addMemberHint') }}</p>
                </div>

                <div class="wz-agent-detail">
                  <template v-if="selectedAgent">
                    <div class="wz-field">
                      <label class="wz-label">{{ $t('wizard.preview.name') }}</label>
                      <Input :modelValue="selectedAgent.name" readonly />
                    </div>
                    <div class="wz-field">
                      <label class="wz-label">{{ $t('wizard.preview.roleTag') }}</label>
                      <Input :modelValue="selectedAgent.roleTag || ''" readonly />
                    </div>
                    <template v-if="selectedAgent.isMain || selectedAgent.prompt">
                      <div class="wz-field">
                        <div class="wz-label-row">
                          <label class="wz-label">{{ $t('wizard.preview.prompt') }}</label>
                          <PromptVariablesButton @insert="insertAgentVar" />
                          <Button text size="mini" :loading="optimizing" @click="handleOptimize">{{ $t('wizard.preview.optimize') }}</Button>
                        </div>
                        <Textarea ref="agentPromptRef" v-model="selectedAgent.prompt" :rows="10" />
                      </div>
                    </template>
                    <template v-else-if="composing">
                      <p class="wz-hint">{{ $t('wizard.preview.composing') }}</p>
                    </template>
                    <template v-else>
                      <div class="wz-field">
                        <label class="wz-label">{{ $t('wizard.preview.refLabel') }}</label>
                        <Input :modelValue="selectedAgent.ref" readonly />
                      </div>
                      <p class="wz-hint">{{ $t('wizard.preview.refEditHint') }}</p>
                    </template>
                  </template>
                  <div v-else class="wz-empty">{{ $t('wizard.preview.selectHint') }}</div>
                </div>
              </div>
            </template>

            <!-- 组合提示词 tab（三层只读） -->
            <template #prompt>
              <CombinedPromptPreview
                :description="composeDescription()"
                :agents="agents"
                :agent="selectedAgent"
              />
            </template>

            <!-- 项目记忆 tab -->
            <template #memory>
              <div class="wz-pv-split">
                <div class="wz-agent-list">
                  <div class="wz-group-h">{{ $t('wizard.memory.presetGroup', { n: presetMemoryCount }) }}<span class="wz-legend-note">{{ $t('wizard.memory.presetNote') }}</span></div>
                  <div
                    v-for="(m, idx) in presetMemories"
                    :key="m.id"
                    class="wz-agent-item"
                    :class="{ on: selectedMemIdx === idx }"
                    @click="selectedMemIdx = idx"
                  >
                    <b>{{ m.name }}</b>
                    <span class="wz-src" :class="srcClass(m.source)">{{ sourceLabel(m.source) }}</span>
                  </div>
                  <div class="wz-group-h">{{ $t('wizard.memory.customGroup') }}</div>
                  <div
                    v-for="(m, idx) in customMemories"
                    :key="m.id"
                    class="wz-agent-item"
                    :class="{ on: selectedMemIdx === presetMemories.length + idx }"
                    @click="selectedMemIdx = presetMemories.length + idx"
                  >
                    <b>{{ m.name }}</b>
                    <button type="button" class="wz-x" :title="$t('wizard.common.remove')" @click.stop="removeMemory(presetMemories.length + idx)">✕</button>
                  </div>
                  <Button text size="mini" @click="addMemoryCategory">{{ $t('wizard.common.addCategory') }}</Button>
                </div>

                <div class="wz-agent-detail">
                  <template v-if="selectedMemory">
                    <Tabs class="wz-mem-tabs" :tabs="memTabs" v-model="memTab">
                      <template #content>
                        <Textarea v-model="selectedMemory.content" :rows="10" />
                        <p class="wz-hint">{{ $t('wizard.memory.contentHint') }}</p>
                      </template>
                      <template #prompt>
                        <div class="wz-label-row">
                          <PromptVariablesButton @insert="insertMemoryVar" />
                        </div>
                        <Textarea ref="memoryPromptRef" v-model="selectedMemory.prompt" :rows="10" :placeholder="$t('wizard.memory.promptHint')" />
                        <p class="wz-hint">{{ $t('wizard.memory.promptHint') }}</p>
                      </template>
                    </Tabs>
                  </template>
                </div>
              </div>
            </template>
          </Tabs>

          <div v-if="generating" class="wz-generating">
            <span class="wz-spinner" />{{ $t('wizard.preview.generating') }}
            <span class="wz-hint">{{ $t('wizard.preview.generateHint') }}</span>
          </div>
        </section>
      </div>
    </div>

    <!-- 底栏（自管；DialogManager 仅转发默认插槽） -->
    <div class="wz-footer">
      <Button size="small" :disabled="step === 1" @click="prev">{{ $t('wizard.common.prev') }}</Button>
      <Button size="small" type="primary" :loading="generating" @click="onPrimary">
        {{ step === 5 ? $t('wizard.common.generate') : $t('wizard.common.next') }}
      </Button>
      <span class="wz-footer-grow" />
      <Button size="small" @click="savePreset">{{ $t('wizard.common.savePreset') }}</Button>
      <Button size="small" @click="closeDialog">{{ $t('wizard.common.close') }}</Button>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button, Input, Textarea, Switch, Tooltip, Tabs, message, promptInput } from '../../components/ui'
import CombinedPromptPreview from './CombinedPromptPreview.vue'
import PromptVariablesButton from '../../components/common/PromptVariablesButton.vue'
import { useVariableInsert } from '../../composables/usePromptVariables'
import { optimizeAgentPrompt } from '../../api/config'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { MsgTopics } from '../../events/msgkeys'

const { t } = useI18n()

const props = defineProps({
  reason: { type: String, default: '' },
  workDir: { type: String, default: '' },
  specPath: { type: String, default: '' },
  // 关闭对话框（由 scenarioWizard.js 注入 → DialogManager handle.close）
  requestClose: { type: Function, default: null },
  // 已关闭（onUnmounted）→ 复位防重入 flag
  notifyClosed: { type: Function, default: null },
})

const OTHER = '__other__'

// ── 选项常量（值为稳定标识，label 为 i18n key）─────────────────
const PTYPE_OPTS = [
  { value: 'biz', label: 'wizard.stack.ptype.biz', desc: 'wizard.stack.ptype.bizDesc', team: 'wizard.stack.ptype.bizTeam' },
  { value: 'component', label: 'wizard.stack.ptype.component', desc: 'wizard.stack.ptype.componentDesc', team: 'wizard.stack.ptype.componentTeam' },
  { value: 'infra', label: 'wizard.stack.ptype.infra', desc: 'wizard.stack.ptype.infraDesc', team: 'wizard.stack.ptype.infraTeam' },
  { value: 'game', label: 'wizard.stack.ptype.game', desc: 'wizard.stack.ptype.gameDesc', team: 'wizard.stack.ptype.gameTeam' },
]
const PLATFORM_OPTS = [
  { value: 'frontend', label: 'wizard.stack.platform.frontend' },
  { value: 'backend', label: 'wizard.stack.platform.backend' },
  { value: 'web', label: 'wizard.stack.platform.web', impliesFront: true },
  { value: 'mobile', label: 'wizard.stack.platform.mobile', impliesFront: true },
  { value: 'miniapp', label: 'wizard.stack.platform.miniapp', impliesFront: true },
]
const FE_LANG_OPTS = [
  { value: 'ts', label: 'wizard.stack.feLang.ts' },
  { value: 'js', label: 'wizard.stack.feLang.js' },
]
const FE_FW_OPTS = [
  { value: 'vue', label: 'wizard.stack.feFw.vue' },
  { value: 'react', label: 'wizard.stack.feFw.react' },
  { value: 'svelte', label: 'wizard.stack.feFw.svelte' },
  { value: 'next', label: 'wizard.stack.feFw.next' },
  { value: 'nuxt', label: 'wizard.stack.feFw.nuxt' },
  { value: 'vanilla', label: 'wizard.stack.feFw.vanilla' },
]
const FE_UI_OPTS = [
  { value: 'tailwind', label: 'wizard.stack.feUi.tailwind' },
  { value: 'shadcn', label: 'wizard.stack.feUi.shadcn' },
  { value: 'antd', label: 'wizard.stack.feUi.antd' },
  { value: 'element', label: 'wizard.stack.feUi.element' },
  { value: 'none', label: 'wizard.stack.feUi.none' },
]
const BE_LANG_OPTS = [
  { value: 'go', label: 'wizard.stack.beLang.go' },
  { value: 'node', label: 'wizard.stack.beLang.node' },
  { value: 'python', label: 'wizard.stack.beLang.python' },
  { value: 'java', label: 'wizard.stack.beLang.java' },
  { value: 'rust', label: 'wizard.stack.beLang.rust' },
]
const BE_FW_OPTS = [
  { value: 'gin', label: 'wizard.stack.beFw.gin' },
  { value: 'fiber', label: 'wizard.stack.beFw.fiber' },
  { value: 'nestjs', label: 'wizard.stack.beFw.nestjs' },
  { value: 'fastapi', label: 'wizard.stack.beFw.fastapi' },
  { value: 'spring', label: 'wizard.stack.beFw.spring' },
  { value: 'axum', label: 'wizard.stack.beFw.axum' },
]
const DB_OPTS = [
  { value: 'postgres', label: 'wizard.stack.db.postgres' },
  { value: 'mysql', label: 'wizard.stack.db.mysql' },
  { value: 'mongo', label: 'wizard.stack.db.mongo' },
  { value: 'sqlite', label: 'wizard.stack.db.sqlite' },
  { value: 'none', label: 'wizard.stack.db.none' },
]
const ORM_OPTS = [
  { value: 'prisma', label: 'wizard.stack.orm.prisma' },
  { value: 'drizzle', label: 'wizard.stack.orm.drizzle' },
  { value: 'typeorm', label: 'wizard.stack.orm.typeorm' },
  { value: 'gorm', label: 'wizard.stack.orm.gorm' },
  { value: 'native', label: 'wizard.stack.orm.native' },
]
const ARCH_OPTS = [
  { value: 'two', label: 'wizard.stack.arch.two', desc: 'wizard.stack.arch.twoDesc', team: 'wizard.stack.arch.twoTeam' },
  { value: 'three', label: 'wizard.stack.arch.three', desc: 'wizard.stack.arch.threeDesc', team: 'wizard.stack.arch.threeTeam' },
  { value: 'micro', label: 'wizard.stack.arch.micro', desc: 'wizard.stack.arch.microDesc', team: 'wizard.stack.arch.microTeam' },
  { value: 'serverless', label: 'wizard.stack.arch.serverless', desc: 'wizard.stack.arch.serverlessDesc', team: 'wizard.stack.arch.serverlessTeam' },
]
const B_ACCESS_OPTS = [
  { value: 'local', label: 'wizard.stack.b.accessLocal', desc: 'wizard.stack.b.accessLocalDesc' },
  { value: 'git', label: 'wizard.stack.b.accessGit', desc: 'wizard.stack.b.accessGitDesc' },
  { value: 'ide', label: 'wizard.stack.b.accessIde', desc: 'wizard.stack.b.accessIdeDesc' },
]
const B_SCOPE_OPTS = [
  { value: 'module', label: 'wizard.stack.b.scopeModule' },
  { value: 'cross', label: 'wizard.stack.b.scopeCross' },
  { value: 'repo', label: 'wizard.stack.b.scopeRepo' },
]
const B_DEP_OPTS = [
  { value: 'no', label: 'wizard.stack.b.depNo' },
  { value: 'yes', label: 'wizard.stack.b.depYes' },
  { value: 'sec', label: 'wizard.stack.b.depSec' },
]
const B_TEST_OPTS = [
  { value: 'auto', label: 'wizard.stack.b.testAuto' },
  { value: 'manual', label: 'wizard.stack.b.testManual' },
]
const B_BRANCH_OPTS = [
  { value: 'yes', label: 'wizard.stack.b.branchYes' },
  { value: 'no', label: 'wizard.stack.b.branchNo' },
]
const B_RHYTHM_OPTS = [
  { value: 'vibe', label: 'wizard.stack.b.rhythmVibe' },
  { value: 'spec', label: 'wizard.stack.b.rhythmSpec' },
]
const C_TARGET_OPTS = [
  { value: 'structure', label: 'wizard.stack.c.targetStructure', desc: 'wizard.stack.c.targetStructureDesc' },
  { value: 'arch', label: 'wizard.stack.c.targetArch', desc: 'wizard.stack.c.targetArchDesc' },
  { value: 'stack', label: 'wizard.stack.c.targetStack', desc: 'wizard.stack.c.targetStackDesc' },
  { value: 'debt', label: 'wizard.stack.c.targetDebt', desc: 'wizard.stack.c.targetDebtDesc' },
  { value: 'security', label: 'wizard.stack.c.targetSecurity', desc: 'wizard.stack.c.targetSecurityDesc' },
]
const C_KEEP_OPTS = [
  { value: 'strict', label: 'wizard.stack.c.keepStrict', desc: 'wizard.stack.c.keepStrictDesc' },
  { value: 'compat', label: 'wizard.stack.c.keepCompat', desc: 'wizard.stack.c.keepCompatDesc' },
  { value: 'breaking', label: 'wizard.stack.c.keepBreaking', desc: 'wizard.stack.c.keepBreakingDesc' },
]
const C_RANGE_OPTS = [
  { value: 'file', label: 'wizard.stack.c.rangeFile' },
  { value: 'module', label: 'wizard.stack.c.rangeModule' },
  { value: 'system', label: 'wizard.stack.c.rangeSystem' },
]
const C_VERIFY_OPTS = [
  { value: 'existing', label: 'wizard.stack.c.verifyExisting' },
  { value: 'character', label: 'wizard.stack.c.verifyCharacter' },
  { value: 'manual', label: 'wizard.stack.c.verifyManual' },
  { value: 'self', label: 'wizard.stack.c.verifySelf' },
]
const C_ROLE_OPTS = [
  { value: 'legacy', label: 'wizard.stack.c.roleLegacy' },
  { value: 'guardian', label: 'wizard.stack.c.roleGuardian' },
  { value: 'migration', label: 'wizard.stack.c.roleMigration' },
]
const C_TLANG_OPTS = [
  { value: 'go', label: 'wizard.stack.beLang.go' },
  { value: 'java', label: 'wizard.stack.beLang.java' },
  { value: 'rust', label: 'wizard.stack.beLang.rust' },
]
// 预置 8 类记忆（id 稳定；source: scan / llm / todo）
const PRESET_MEMORY = [
  { id: 'project-overview', key: 'wizard.memory.catProjectOverview', source: 'llm' },
  { id: 'common-lib', key: 'wizard.memory.catCommonLib', source: 'llm' },
  { id: 'dev-convention', key: 'wizard.memory.catDevConvention', source: 'scan' },
  { id: 'build-release', key: 'wizard.memory.catBuildRelease', source: 'scan' },
  { id: 'api-lib', key: 'wizard.memory.catApiLib', source: 'scan' },
  { id: 'test-convention', key: 'wizard.memory.catTestConvention', source: 'scan' },
  { id: 'typical-ref', key: 'wizard.memory.catTypicalRef', source: 'todo' },
  { id: 'user-decision', key: 'wizard.memory.catUserDecision', source: 'todo' },
]
// 预置智能体名 → 角色标签（名称 = capability/agents/<名>.agent.md，保持中文文件标识）
const AGENT_ROLE_TAGS = {
  主协调者: '主', 需求分析师: '需求', 架构设计师: '架构', 'UX 设计师': 'UX',
  前端开发: '前端', 后端开发: '后端', 数据工程师: '数据', 集成协调者: '集成',
  移动端开发: '移动', 'DevOps 工程师': '部署', 'UI 测试工程师': 'UI测试',
  前端测试工程师: '前端测试', 后端测试工程师: '后端测试', 系统测试工程师: '系统测试',
  代码审查: '审查', 安全审查员: '安全', 性能优化师: '性能', 调试工程师: '调试',
  依赖审计员: '依赖', 侦察工程师: '侦察', 遗留分析员: '遗留', 兼容守护: '兼容',
  迁移规划师: '迁移', 文档撰写者: '文档',
}

// ── 状态 ────────────────────────────────────────────────────
const step = ref(1)
const probe = ref(null)
const probeLoading = ref(false)

const info = reactive({ goal: '', summary: '', budget: '', deploy: '', period: '', background: '', requirement: '' })
const mode = ref('A')
const modeOther = ref('')
const sel = reactive({
  ptype: 'biz',
  platforms: ['frontend'],
  feLangs: ['ts'],
  feFw: 'vue',
  feUi: 'none',
  beLangs: ['go'],
  beFw: 'gin',
  db: 'postgres',
  orm: 'gorm',
  arch: 'three',
  bAccess: 'local',
  bScope: 'module',
  bDep: 'no',
  bTest: 'auto',
  bBranch: 'no',
  bRhythm: 'spec',
  cTarget: 'structure',
  cTlang: 'go',
  cKeep: 'strict',
  cRange: 'module',
  cVerify: ['existing'],
  cRoles: ['legacy'],
})
const custom = reactive({
  ptype: '', platforms: '', feLang: '', feFw: '', feUi: '', beLang: '', beFw: '', db: '', orm: '', arch: '',
  bAccess: '', cTarget: '', cTlang: '',
})

const summaryEdits = reactive({ summaryText: '', archText: '', langText: '' })
const team = ref([])
const aux = reactive({ memory: true, userPref: true, codegraph: false, vfts: false, fileHistory: false, initGit: false })

// 仅工作目录未检测到 git 时提供「初始化 git」（01 §6.1）；据此决定 init_git 是否可勾选。
const gitOffered = computed(() => !!(probe.value && probe.value.has_git === false))

const previewTab = ref('scene')
const sceneName = ref('')
const agents = ref([])
const selectedAgentIdx = ref(0)
const optimizing = ref(false)
// E-27：优化订阅句柄（内部 3 个 mq.on + guiReq 兜底仅在 done/error 自退订）→ 卸载时主动 abort，
// 防止流进行中关闭弹框后订阅残留（闭包持组件 ref，残留 onToken 会写回已关闭表单）。
let optimizeAbort = null
// 提示词合成（agent-wizard-compose）进行中；用于预览占位与按钮态。
const composing = ref(false)

const memoryCats = ref([])
const selectedMemIdx = ref(0)
const memTab = ref('content')

const generating = ref(false)

// ── 计算属性 ────────────────────────────────────────────────
const wizardSteps = computed(() => ([
  { n: 1, label: t('wizard.steps.1') },
  { n: 2, label: t('wizard.steps.2') },
  { n: 3, label: t('wizard.steps.3') },
  { n: 4, label: t('wizard.steps.4') },
  { n: 5, label: t('wizard.steps.5') },
]))
const stepLabel = computed(() => (wizardSteps.value[step.value - 1] || {}).label || '')

const stackBanner = computed(() => {
  if (mode.value === 'A') return t('wizard.stack.bannerA')
  if (mode.value === 'B') return t('wizard.stack.bannerB')
  if (mode.value === 'C') return t('wizard.stack.bannerC')
  return t('wizard.stack.bannerOther')
})

const previewTabs = computed(() => [
  { name: 'scene', label: t('wizard.preview.tabScene') },
  { name: 'prompt', label: t('wizard.preview.tabPrompt') },
  { name: 'memory', label: t('wizard.preview.tabMemory', { n: memoryCats.value.length }) },
])
const memTabs = computed(() => [
  { name: 'content', label: t('wizard.memory.tabContent') },
  { name: 'prompt', label: t('wizard.memory.tabPrompt') },
])

const mainAgents = computed(() => agents.value.filter(a => a.isMain))
const subAgents = computed(() => agents.value.filter(a => !a.isMain))
const selectedAgent = computed(() => agents.value[selectedAgentIdx.value] || null)

const presetMemories = computed(() => memoryCats.value.filter(m => m.preset))
const customMemories = computed(() => memoryCats.value.filter(m => !m.preset))
const presetMemoryCount = computed(() => presetMemories.value.length)
const selectedMemory = computed(() => memoryCats.value[selectedMemIdx.value] || null)

// 变量插入（OP-12）：agent 提示词 / 记忆沉淀提示词各自光标处插入 {{...}}。
const agentPromptRef = ref(null)
const memoryPromptRef = ref(null)
const { insert: insertAgentVar } = useVariableInsert({
  getEl: () => agentPromptRef.value?.$el || null,
  getValue: () => (selectedAgent.value && selectedAgent.value.prompt) || '',
  setValue: (v) => { const a = selectedAgent.value; if (a) a.prompt = v },
})
const { insert: insertMemoryVar } = useVariableInsert({
  getEl: () => memoryPromptRef.value?.$el || null,
  getValue: () => (selectedMemory.value && selectedMemory.value.prompt) || '',
  setValue: (v) => { const m = selectedMemory.value; if (m) m.prompt = v },
})

const reconText = computed(() => {
  const p = probe.value
  if (!p || (p.empty && !p.has_code)) return t('wizard.stack.b.reconEmpty')
  const parts = []
  if (p.languages && p.languages.length) parts.push('语言 ' + p.languages.join(' / '))
  if (p.frameworks && p.frameworks.length) parts.push('框架 ' + p.frameworks.join(' / '))
  if (p.package_manager) parts.push('包管理 ' + p.package_manager)
  if (p.build_tool) parts.push('构建 ' + p.build_tool)
  if (p.test_tool) parts.push('测试 ' + p.test_tool)
  if (p.lint_tool) parts.push('lint ' + p.lint_tool)
  return parts.join(' · ') || t('wizard.stack.b.reconEmpty')
})
const currentStackText = computed(() => {
  const p = probe.value
  if (!p) return '-'
  const parts = []
  if (p.languages && p.languages.length) parts.push(p.languages.join('/'))
  if (p.frameworks && p.frameworks.length) parts.push(p.frameworks.join('/'))
  return parts.join(' · ') || '-'
})

// ── 通用小工具 ──────────────────────────────────────────────
function withOther(base) {
  return [...base, { value: OTHER, label: 'wizard.common.other' }]
}
function toggleArr(arr, v) {
  const i = arr.indexOf(v)
  if (i >= 0) arr.splice(i, 1)
  else arr.push(v)
}
function onPlatformToggle(o) {
  toggleArr(sel.platforms, o.value)
  if (o.impliesFront && sel.platforms.includes(o.value) && !sel.platforms.includes('frontend')) {
    sel.platforms.push('frontend')
  }
}
function setMode(m) {
  mode.value = m
}
function labelOf(opts, value, customText) {
  if (value === OTHER) return customText || t('wizard.common.other')
  const o = opts.find(x => x.value === value)
  return o ? t(o.label) : (value || '')
}
function labelsOf(opts, values, customText) {
  return (values || []).map(v => labelOf(opts, v, customText)).filter(Boolean)
}
function sourceLabel(src) {
  if (src === 'scan') return t('wizard.memory.srcScan')
  if (src === 'llm') return t('wizard.memory.srcLlm')
  return t('wizard.memory.srcTodo')
}
function srcClass(src) {
  if (src === 'scan') return 'hi'
  if (src === 'todo') return 'lo'
  return ''
}
function slugify(name) {
  return String(name || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .replace(/-{2,}/g, '-')
}
function defaultSceneName() {
  const wd = (props.workDir || '').replace(/[\\/]+$/, '')
  const base = wd.split(/[\\/]/).pop() || ''
  return slugify(base) || 'project-team'
}
/** 子 agent 引用路径（出厂预置；后端按 ${exeDir} 解析）。 */
function agentRef(name) {
  return '${exeDir}/capability/agents/' + name + '.agent.md'
}

// ── 探测（agent-wizard-probe）───────────────────────────────
async function runProbe() {
  probeLoading.value = true
  try {
    const env = await mq.emit(EventNames.agentWizardProbe, {})
    const p = env && env.backend && env.backend.result && env.backend.result.probe
    if (p) {
      probe.value = p
      // 有代码 → 默认迭代；空目录 → 默认新构建
      if (p.empty === false && p.has_code) mode.value = 'B'
      else if (p.empty === true) mode.value = 'A'
      prefillFromProbe(p)
    }
  } catch (_) { /* 探测失败不阻断向导 */ } finally {
    probeLoading.value = false
  }
}
function prefillFromProbe(p) {
  if (!p || p.empty) return
  if (!info.summary.trim()) {
    const parts = []
    if (p.languages && p.languages.length) parts.push(p.languages.join('/'))
    if (p.frameworks && p.frameworks.length) parts.push(p.frameworks.join('/'))
    if (parts.length) info.summary = parts.join(' · ')
  }
  if (!info.deploy.trim() && p.has_git) info.deploy = 'git'
}

// ── 摘要 / 团队 ────────────────────────────────────────────
function deriveTeam() {
  const members = []
  const push = (name) => {
    if (name && !members.some(m => m.name === name)) members.push({ name, roleTag: AGENT_ROLE_TAGS[name] || '' })
  }
  push('主协调者')
  push('代码审查')
  push('系统测试工程师')
  const iter = mode.value === 'B' || mode.value === 'C'
  if (sel.platforms.includes('frontend') || sel.platforms.includes('web') || iter) push('前端测试工程师')
  if (sel.platforms.includes('backend') || iter) push('后端测试工程师')
  if (mode.value === 'A') {
    push('需求分析师')
    push('架构设计师')
    const archMap = {
      two: ['前端开发', '后端开发'],
      three: ['前端开发', '后端开发', '数据工程师'],
      micro: ['前端开发', '后端开发', '集成协调者', '数据工程师'],
      serverless: ['前端开发', '后端开发', 'DevOps 工程师'],
    }
    for (const n of (archMap[sel.arch] || [])) push(n)
    if (sel.ptype === 'component') push('文档撰写者')
    if (sel.ptype === 'infra') { push('DevOps 工程师'); push('安全审查员'); push('性能优化师') }
    if (sel.ptype === 'game') push('UX 设计师')
    if (sel.platforms.includes('mobile') || sel.platforms.includes('miniapp')) push('移动端开发')
    if (sel.platforms.includes('frontend') || sel.platforms.includes('web')) { push('UX 设计师'); push('UI 测试工程师') }
  } else if (mode.value === 'B') {
    push('侦察工程师')
    push('前端开发')
    push('后端开发')
  } else if (mode.value === 'C') {
    push('侦察工程师')
    push('前端开发')
    push('后端开发')
    push('遗留分析员')
    push('兼容守护')
    if (sel.cTarget === 'stack') push('迁移规划师')
    if (sel.cTarget === 'security') push('安全审查员')
  }
  return members.map((m, i) => ({ ...m, isMain: i === 0 }))
}
function ensureSummary() {
  if (!summaryEdits.summaryText) summaryEdits.summaryText = info.summary || info.goal
  if (!summaryEdits.archText) {
    summaryEdits.archText = mode.value === 'A' ? labelOf(ARCH_OPTS, sel.arch, custom.arch) : currentStackText.value
  }
  if (!summaryEdits.langText) summaryEdits.langText = composeLangText()
  if (team.value.length === 0) team.value = deriveTeam()
}
function composeLangText() {
  if (mode.value === 'A') {
    const fe = [...labelsOf(FE_LANG_OPTS, sel.feLangs, custom.feLang), labelOf(FE_FW_OPTS, sel.feFw, custom.feFw)]
    const be = [...labelsOf(BE_LANG_OPTS, sel.beLangs, custom.beLang), labelOf(BE_FW_OPTS, sel.beFw, custom.beFw)]
    return [...fe, ...be].filter(Boolean).join(' + ')
  }
  return currentStackText.value
}
function removeTeamMember(idx) {
  if (team.value[idx] && !team.value[idx].isMain) team.value.splice(idx, 1)
}
async function addTeamMember() {
  const name = await promptInput(t('wizard.summary.addMemberPrompt'))
  if (name === null) return
  const n = String(name).trim()
  if (!n) return
  if (team.value.some(m => m.name === n)) return
  team.value.push({ name: n, roleTag: AGENT_ROLE_TAGS[n] || '', isMain: false })
}

// ── 预览 / 记忆 ────────────────────────────────────────────
function defaultMainPrompt() {
  return '你是本项目的编排者与决策者：规划任务 → 委派合适成员 → 检查结果 → 决策 → 循环，直到目标达成。' +
    '遵循「先读后改、最小变更、根因修复」原则，变更前先理解现有代码与约定。'
}
function ensurePreview() {
  if (!sceneName.value.trim()) sceneName.value = defaultSceneName()
  if (team.value.length === 0) ensureSummary()
  if (agents.value.length === 0) {
    const derived = team.value.length ? team.value : deriveTeam()
    agents.value = [
      { name: '主协调者', roleTag: 'main', isMain: true, prompt: defaultMainPrompt(), ref: '' },
      // 子 agent 占位：ref 置 ''，待合成回填 prompt（异步）；失败回落预置引用（见 composeAgents）。
      ...derived.filter(m => !m.isMain).map(m => ({ name: m.name, roleTag: m.roleTag || '', isMain: false, prompt: '', ref: '' })),
    ]
    selectedAgentIdx.value = 0
  }
  if (memoryCats.value.length === 0) buildMemory()
  ensureCompose()
}

// ── 提示词合成（agent-wizard-compose）────────────────────────
// 进入预览步对当前团队发起一次合成：按项目把每个成员的提示词内联进场景（子 agent ref 置 ''、
// prompt 走合成结果）；失败/超时回落为「预置引用」，且不阻断向导流程。
const COMPOSE_TIMEOUT = 30000
let composeSig = '' // 已发起合成的团队签名（同团队只发起一次）
let composeSeq = 0 // 并发序号：仅最后一次合成的结果回填
let composePromise = null // 在飞的合成（生成前等待其结束，避免用到占位）

/** 团队签名（名字 + 角色标签）；用于同团队去重。 */
function teamSignature(list) {
  return (list || []).map(m => m.name + ':' + (m.roleTag || '')).join('|')
}
/** 向导模式 → 合成契约枚举（第四模式 other → custom）。 */
function composeMode() {
  return mode.value === 'other' ? 'custom' : mode.value
}
/** 合成结果回填：命中的成员内联 prompt（ref 置 ''）；未命中子 agent 回落预置引用。 */
function applyComposed(list) {
  const byName = new Map()
  for (const a of list) if (a && a.name) byName.set(a.name, a)
  for (const a of agents.value) {
    const c = byName.get(a.name)
    if (c && typeof c.prompt === 'string' && c.prompt.trim()) {
      a.prompt = c.prompt
      a.ref = ''
    } else if (a.isMain) {
      if (!a.prompt || !a.prompt.trim()) a.prompt = defaultMainPrompt()
    } else {
      a.prompt = ''
      a.ref = agentRef(a.name)
    }
  }
}
/** 合成失败回落：主 agent 用默认提示词，子 agent 回到预置引用。 */
function fallbackComposed() {
  for (const a of agents.value) {
    if (a.isMain) {
      if (!a.prompt || !a.prompt.trim()) a.prompt = defaultMainPrompt()
    } else {
      a.prompt = ''
      a.ref = agentRef(a.name)
    }
  }
}
/** 发起一次合成（同团队只发起一次）；返回值 = 在飞的 Promise（供生成前 await）。 */
function ensureCompose() {
  const list = team.value.length ? team.value : deriveTeam()
  if (!list.length) return null
  const sig = teamSignature(list)
  if (sig === composeSig) return composePromise
  composeSig = sig
  const seq = ++composeSeq
  composing.value = true
  composePromise = composeAgents(list, seq)
  return composePromise
}
async function composeAgents(list, seq) {
  const req = {
    mode: composeMode(),
    description: composeDescription(),
    probe: probe.value,
    choices: sel,
    team: list.map(m => ({ name: m.name, roletag: m.roleTag || '' })),
  }
  try {
    const env = await mq.emit(EventNames.agentWizardCompose, req, { timeout: COMPOSE_TIMEOUT })
    const res = env && env.backend && env.backend.result
    if (!res || res.ok === false || !Array.isArray(res.agents)) {
      throw new Error((res && res.error) || 'compose failed')
    }
    if (seq !== composeSeq) return
    applyComposed(res.agents)
  } catch (_) {
    if (seq !== composeSeq) return
    fallbackComposed()
    message.warning(t('wizard.preview.composeFailed'))
  } finally {
    if (seq === composeSeq) composing.value = false
  }
}
function buildMemory() {
  const p = probe.value || {}
  const mk = (id, name, source, content) => ({ id, name, preset: true, source, content, prompt: '' })
  const arr = PRESET_MEMORY.map(m => mk(m.id, t(m.key), m.source, ''))
  const byId = (id) => arr.find(m => m.id === id)
  const ov = byId('project-overview')
  if (ov) ov.content = summaryEdits.summaryText || info.summary || info.goal || ''
  const dev = byId('dev-convention')
  if (dev && p.lint_tool) { dev.content = 'lint: ' + p.lint_tool; dev.source = 'scan' }
  const build = byId('build-release')
  if (build) {
    const lines = []
    if (p.build_tool) lines.push('构建: ' + p.build_tool)
    if (p.package_manager) lines.push('包管理: ' + p.package_manager)
    if (info.deploy) lines.push('部署: ' + info.deploy)
    if (lines.length) { build.content = lines.join('\n'); build.source = 'scan' }
  }
  const api = byId('api-lib')
  if (api && p.frameworks && p.frameworks.length) { api.content = p.frameworks.join(' / '); api.source = 'scan' }
  const test = byId('test-convention')
  if (test && p.test_tool) { test.content = '测试框架: ' + p.test_tool; test.source = 'scan' }
  const lib = byId('common-lib')
  if (lib && p.top_dirs && p.top_dirs.length) lib.content = p.top_dirs.join(' / ')
  memoryCats.value = arr
  selectedMemIdx.value = 0
}
function removeMemory(idx) {
  if (memoryCats.value[idx] && !memoryCats.value[idx].preset) {
    memoryCats.value.splice(idx, 1)
    if (selectedMemIdx.value >= memoryCats.value.length) selectedMemIdx.value = memoryCats.value.length - 1
  }
}
async function addMemoryCategory() {
  const name = await promptInput(t('wizard.memory.addCategoryPrompt'))
  if (name === null) return
  const n = String(name).trim()
  if (!n) return
  memoryCats.value.push({ id: 'custom-' + Date.now(), name: n, preset: false, source: 'todo', content: '', prompt: '' })
  selectedMemIdx.value = memoryCats.value.length - 1
}

// ── Agent 增删 / 提示词优化 ─────────────────────────────────
function addSubAgent() {
  const used = new Set(agents.value.map(a => a.name))
  const candidate = Object.keys(AGENT_ROLE_TAGS).find(n => n !== '主协调者' && !used.has(n))
  if (!candidate) return
  agents.value.push({ name: candidate, roleTag: AGENT_ROLE_TAGS[candidate] || '', isMain: false, prompt: '', ref: agentRef(candidate) })
  selectedAgentIdx.value = agents.value.length - 1
}
function removeAgent(idx) {
  const a = agents.value[idx]
  if (!a || a.isMain) return
  agents.value.splice(idx, 1)
  if (selectedAgentIdx.value >= agents.value.length) selectedAgentIdx.value = agents.value.length - 1
}
function handleOptimize() {
  if (optimizing.value) return
  const idx = selectedAgentIdx.value
  const a = agents.value[idx]
  if (!a) return
  if (!a.prompt || !a.prompt.trim()) { message.warning(t('wizard.common.inputRequired')); return }
  optimizing.value = true
  optimizeAbort = optimizeAgentPrompt(
    { title: t('wizard.preview.optimizeTitle', { name: a.name }), useCase: 'agent', prompt: a.prompt },
    (chunk) => { const cur = agents.value[idx]; if (cur) cur.prompt = (cur.prompt || '') + chunk },
    (finalPrompt) => {
      optimizing.value = false
      const cur = agents.value[idx]
      if (cur && finalPrompt) cur.prompt = finalPrompt
    },
    (err) => { optimizing.value = false; message.error(t('wizard.preview.optimizeFailed') + ': ' + err) },
  )
}

// ── 描述 / project_spec 合成 ────────────────────────────────
function modeLabel() {
  if (mode.value === 'A') return t('wizard.spec.modeGreenfield')
  if (mode.value === 'B') return t('wizard.spec.modeIterate')
  if (mode.value === 'C') return t('wizard.spec.modeRefactor')
  return t('wizard.spec.modeCustom')
}
function composeDescription() {
  const goal = info.goal.trim()
  if (mode.value === 'A') {
    const platforms = labelsOf(PLATFORM_OPTS, sel.platforms, custom.platforms).join('、') || t('wizard.desc.unspecified')
    const arch = labelOf(ARCH_OPTS, sel.arch, custom.arch)
    const fe = [...labelsOf(FE_LANG_OPTS, sel.feLangs, custom.feLang), labelOf(FE_FW_OPTS, sel.feFw, custom.feFw)]
    if (sel.feUi !== 'none') fe.push(labelOf(FE_UI_OPTS, sel.feUi, custom.feUi))
    const be = [...labelsOf(BE_LANG_OPTS, sel.beLangs, custom.beLang), labelOf(BE_FW_OPTS, sel.beFw, custom.beFw)]
    if (sel.db !== 'none') be.push(labelOf(DB_OPTS, sel.db, custom.db))
    be.push(labelOf(ORM_OPTS, sel.orm, custom.orm))
    return t('wizard.desc.greenfield', {
      platforms, arch,
      fe: fe.filter(Boolean).join(' / '),
      be: be.filter(Boolean).join(' / '),
      goal,
    })
  }
  const stack = currentStackText.value
  if (mode.value === 'B') return t('wizard.desc.iterate', { stack, goal })
  if (mode.value === 'C') {
    return t('wizard.desc.refactor', {
      stack,
      target: labelOf(C_TARGET_OPTS, sel.cTarget, custom.cTarget),
      keep: labelOf(C_KEEP_OPTS, sel.cKeep),
      goal,
    })
  }
  return t('wizard.desc.custom', { text: modeOther.value.trim() || goal })
}
function buildProjectSpecMarkdown() {
  const lines = []
  lines.push('# ' + t('wizard.spec.title'))
  lines.push('')
  lines.push('- ' + t('wizard.spec.generatedAt') + ': ' + new Date().toISOString())
  lines.push('- ' + t('wizard.spec.mode') + ': ' + modeLabel())
  lines.push('- ' + t('wizard.spec.workDir') + ': ' + (props.workDir || '-'))
  lines.push('')
  lines.push('## ' + t('wizard.spec.infoSection'))
  if (info.goal) lines.push('- ' + t('wizard.spec.goal') + ': ' + info.goal)
  if (info.summary) lines.push('- ' + t('wizard.spec.summary') + ': ' + info.summary)
  if (info.budget) lines.push('- ' + t('wizard.spec.budget') + ': ' + info.budget)
  if (info.deploy) lines.push('- ' + t('wizard.spec.deploy') + ': ' + info.deploy)
  if (info.period) lines.push('- ' + t('wizard.spec.period') + ': ' + info.period)
  if (info.background) lines.push('- ' + t('wizard.spec.background') + ': ' + info.background)
  if (info.requirement) lines.push('- ' + t('wizard.spec.requirement') + ': ' + info.requirement)
  lines.push('')
  lines.push('## ' + t('wizard.spec.stackSection'))
  lines.push(composeDescription())
  lines.push('')
  lines.push('## ' + t('wizard.spec.teamSection'))
  for (const m of agents.value) lines.push('- ' + m.name + (m.isMain ? ' (main)' : ''))
  lines.push('')
  lines.push('## ' + t('wizard.spec.auxSection'))
  lines.push('- ' + t('wizard.summary.auxMemory') + ': ' + (aux.memory ? 'on' : 'off'))
  lines.push('- ' + t('wizard.summary.auxUserPref') + ': ' + (aux.userPref ? 'on' : 'off'))
  lines.push('- ' + t('wizard.summary.auxCodegraph') + ': ' + (aux.codegraph ? 'on' : 'off'))
  lines.push('- ' + t('wizard.summary.auxVfts') + ': ' + (aux.vfts ? 'on' : 'off'))
  lines.push('- ' + t('wizard.summary.auxFileHistory') + ': ' + (aux.fileHistory ? 'on' : 'off'))
  lines.push('')
  return lines.join('\n')
}
function buildPayload() {
  const name = sceneName.value.trim() || defaultSceneName()
  return {
    scenario_id: slugify(name) || defaultSceneName(),
    scene_name: name,
    description: composeDescription(),
    agents: agents.value.map(a => {
      // 有合成提示词 → 内联 prompt 且 ref 置 ''（scenario.json agents 全内联时该键省略，加载端走内联分支）；
      // 未合成/合成失败 → 回落预置引用（agentRef）。
      const inline = !a.isMain && !!(a.prompt && a.prompt.trim())
      return {
        name: a.name,
        roletag: a.isMain ? 'main' : (a.roleTag || ''),
        is_main: !!a.isMain,
        prompt: a.prompt || '',
        ref: a.isMain ? '' : (inline ? '' : (a.ref || agentRef(a.name))),
      }
    }),
    memory: memoryCats.value
      .filter(m => m.content && m.content.trim())
      .map(m => ({ category: m.name, content: m.content })),
    project_spec: buildProjectSpecMarkdown(),
    // 辅助选项 → prj 配置键（字符串 "true"/"false"；01 §6.1 / 07 §6）
    config: {
      'memory.enabled': String(aux.memory),
      'memory.category.用户偏好': String(aux.userPref),
      'enable-codegraph': String(aux.codegraph),
      'enable-vfts': String(aux.vfts),
      'history.enabled': String(aux.fileHistory),
    },
    // 仅「未检测到 git」且用户勾选时执行 git init
    init_git: gitOffered.value && !!aux.initGit,
  }
}

// ── 导航 ────────────────────────────────────────────────────
function canLeaveStep1() {
  if (info.goal.trim()) return true
  message.warning(t('wizard.info.goalRequired'))
  return false
}
function goStep(n) {
  if (n === step.value) return
  if (n > 1 && !canLeaveStep1()) return
  step.value = n
  if (n === 4) ensureSummary()
  if (n === 5) ensurePreview()
}
function prev() {
  if (step.value > 1) step.value -= 1
}
function next() {
  if (!canLeaveStep1()) return
  if (step.value < 5) goStep(step.value + 1)
}
function onPrimary() {
  if (step.value === 5) doGenerate()
  else next()
}

// ── 生成（agent-wizard-generate）────────────────────────────
async function doGenerate() {
  if (generating.value) return
  ensureSummary()
  ensurePreview()
  generating.value = true
  try {
    // 等待在飞的提示词合成结束（成功/失败均已在内部兜底，不阻断生成）。
    if (composePromise) await composePromise
    const env = await mq.emit(EventNames.agentWizardGenerate, buildPayload())
    if (!env || !env.backend) throw new Error(t('wizard.generate.unreachable'))
    const res = env.backend.result || {}
    if (res.ok === false || (Array.isArray(res.errors) && res.errors.length)) {
      throw new Error((res.errors && res.errors[0]) || t('wizard.generate.failed'))
    }
    // 新写入的数据投入 UI：场景列表（既有 scenarioReload 通道）+ 记忆清单
    //（复用既有 data-memory-refresh 订阅路径；未打开记忆视图则无订阅者）。
    mq.emit(EventNames.scenarioReload, {})
    mq.emit(MsgTopics.dataMemoryRefresh, {})
    // 结果字段防御性读取（config_saved / git_initialized 可能缺省）。
    const memorySaved = Array.isArray(res.memory_saved) ? res.memory_saved : []
    const configSaved = Array.isArray(res.config_saved) ? res.config_saved : []
    let okText = t('wizard.generate.successDetail', {
      spec: res.spec_path || '-',
      mem: memorySaved.length,
      cfg: configSaved.length,
    })
    if (res.git_initialized === true) okText += ' · ' + t('wizard.generate.gitInitialized')
    message.success(okText)
    dismissDialog()
  } catch (e) {
    message.error(t('wizard.generate.failed') + ': ' + (e.message || e))
  } finally {
    generating.value = false
  }
}
async function generateDefault() {
  if (generating.value) return
  // 「默认」= 全部默认（LLM 推荐 + 各步默认）一步生成并关闭
  mode.value = probe.value && probe.value.empty === false && probe.value.has_code ? 'B' : 'A'
  applyDefaultSelections()
  await doGenerate()
}
function applyDefaultSelections() {
  Object.assign(sel, {
    ptype: 'biz', platforms: ['frontend'], feLangs: ['ts'], feFw: 'vue', feUi: 'none',
    beLangs: ['go'], beFw: 'gin', db: 'postgres', orm: 'gorm', arch: 'three',
    bAccess: 'local', bScope: 'module', bDep: 'no', bTest: 'auto', bBranch: 'no', bRhythm: 'spec',
    cTarget: 'structure', cTlang: 'go', cKeep: 'strict', cRange: 'module', cVerify: ['existing'], cRoles: ['legacy'],
  })
  team.value = []
  agents.value = []
  memoryCats.value = []
  summaryEdits.summaryText = ''
  summaryEdits.archText = ''
  summaryEdits.langText = ''
  // 团队被重置 → 允许对新团队重新发起提示词合成。
  composeSig = ''
  composePromise = null
}

// ── 预设（localStorage；跨会话预填）─────────────────────────
function presetKey() {
  return 'chonkpilot-wizard-preset:' + (props.workDir || 'default')
}
function savePreset() {
  try {
    const preset = {
      info: { ...info },
      mode: mode.value,
      modeOther: modeOther.value,
      sel: JSON.parse(JSON.stringify(sel)),
      custom: { ...custom },
      aux: { ...aux },
      sceneName: sceneName.value,
      summaryEdits: { ...summaryEdits },
      team: JSON.parse(JSON.stringify(team.value)),
      agents: JSON.parse(JSON.stringify(agents.value)),
    }
    localStorage.setItem(presetKey(), JSON.stringify(preset))
    message.success(t('wizard.preset.saved'))
  } catch (e) {
    message.error(t('wizard.preset.saveFailed') + ': ' + (e.message || e))
  }
}
function loadPreset() {
  try {
    const raw = localStorage.getItem(presetKey())
    if (!raw) return
    const p = JSON.parse(raw)
    if (p.info) Object.assign(info, p.info)
    if (p.mode) mode.value = p.mode
    if (typeof p.modeOther === 'string') modeOther.value = p.modeOther
    if (p.sel) Object.assign(sel, p.sel)
    if (p.custom) Object.assign(custom, p.custom)
    if (p.aux) Object.assign(aux, p.aux)
    if (typeof p.sceneName === 'string') sceneName.value = p.sceneName
    if (p.summaryEdits) Object.assign(summaryEdits, p.summaryEdits)
    if (Array.isArray(p.team) && p.team.length) team.value = p.team
    if (Array.isArray(p.agents) && p.agents.length) agents.value = p.agents
  } catch (_) { /* 预设损坏 → 忽略 */ }
}

// ── 关闭 ────────────────────────────────────────────────────
/** 仅关闭对话框（生成成功后使用；不写文件、不置「稍后」）。 */
function dismissDialog() {
  if (props.requestClose) props.requestClose()
}
/** 【关闭】：丢弃未生成的选择，不写任何文件；通知后端本会话不再自动弹（内存级，不落盘）。 */
function closeDialog() {
  mq.emit(EventNames.agentWizardSkip, {})
  dismissDialog()
}

onMounted(async () => {
  await runProbe()
  loadPreset()
})
onUnmounted(() => {
  if (props.notifyClosed) props.notifyClosed()
  // E-27：卸载即中止优化（退订残留订阅），避免流进行中关闭弹框后 onToken 写回已关闭表单。
  if (optimizeAbort) { optimizeAbort(); optimizeAbort = null }
})
</script>

<style>
/* 对话框内容壳（.dialog-body 由 DialogShell 渲染）：去内边距、禁止外层滚动，由向导自管滚动 */
.dialog-body.wizard-dialog-body {
  padding: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}
</style>

<style scoped>
.wizard-root {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* 顶部工具条 */
.wz-toolbar {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 10px 16px;
  border-bottom: 1px solid var(--border, #dee2e6);
  flex-shrink: 0;
}
.wz-title { font-weight: 600; font-size: 14px; }
.wz-path { color: var(--text-muted, #6b7280); font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 420px; }
.wz-step { margin-left: auto; color: var(--text-muted, #6b7280); font-size: 12px; white-space: nowrap; }

/* 主体：左步骤 + 中内容 */
.wz-main {
  flex: 1;
  min-height: 0;
  display: flex;
  overflow: hidden;
}
.wz-rail {
  width: 176px;
  flex-shrink: 0;
  border-right: 1px solid var(--border, #dee2e6);
  padding: 12px 8px;
  overflow-y: auto;
  background: var(--bg-secondary, #fafafa);
}
.wz-rail-list { list-style: none; margin: 0; padding: 0; }
.wz-rail-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 10px;
  border-radius: 4px;
  color: var(--text-muted, #6b7280);
  margin-bottom: 2px;
  cursor: pointer;
  font-size: 13px;
  border-left: 3px solid transparent;
}
.wz-rail-item:hover { background: var(--bg-hover, #e9ecef); }
.wz-rail-item.on { background: var(--accent-bg); color: var(--text-primary); border-left-color: var(--accent); }
.wz-rail-n {
  width: 18px; height: 18px; line-height: 18px; text-align: center;
  border-radius: 50%; background: var(--bg-surface, #dee2e6); color: var(--text-secondary);
  font-size: 11px; flex: 0 0 auto;
}
.wz-rail-item.done .wz-rail-n { background: var(--success, #2d9f4e); color: #fff; }
.wz-rail-item.on .wz-rail-n { background: var(--accent); color: #fff; }
.wz-rail-label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* 内容区 */
.wz-content { flex: 1; min-width: 0; overflow-y: auto; padding: 16px; }
.wz-panel { display: flex; flex-direction: column; gap: 16px; }
.wz-panel-preview { gap: 12px; height: 100%; min-height: 0; position: relative; }

/* 提示条 */
.wz-banner {
  display: flex; gap: 8px; align-items: flex-start;
  padding: 8px 10px; border: 1px solid var(--border, #dee2e6); border-left: 3px solid var(--accent);
  border-radius: 4px; background: var(--accent-bg); color: var(--text-secondary); font-size: 12px; line-height: 1.6;
}

/* 字段集 */
.wz-fieldset { border: 0; margin: 0; padding: 0; }
.wz-fieldset > legend { font-weight: 600; font-size: 13px; margin-bottom: 8px; padding: 0; }
.wz-legend-note { color: var(--text-muted, #6b7280); font-weight: 400; margin-left: 8px; font-size: 12px; }
.wz-field { margin-top: 12px; }
.wz-label { display: flex; align-items: center; gap: 4px; color: var(--text-secondary); margin-bottom: 4px; font-size: 12px; }
.wz-label-row { display: flex; align-items: center; justify-content: space-between; }
.wz-req { color: var(--danger); }
.wz-row2 { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.wz-hint { color: var(--text-muted, #6b7280); font-size: 12px; line-height: 1.5; }
.wz-sub-label { color: var(--text-muted, #6b7280); font-size: 12px; margin: 8px 0 4px; }
.wz-custom { margin-top: 8px; }

/* label 旁 tooltip 图标 */
.wz-tip {
  display: inline-flex; align-items: center; justify-content: center;
  width: 14px; height: 14px; border: 1px solid var(--border, #dee2e6); border-radius: 50%;
  color: var(--text-muted, #6b7280); font-size: 10px; line-height: 1; cursor: help;
}
.wz-tip:hover { border-color: var(--accent); color: var(--accent); }

/* 单选卡 */
.wz-cards { display: grid; gap: 8px; }
.wz-card {
  position: relative; display: flex; gap: 10px; align-items: flex-start;
  padding: 10px 12px; border: 1px solid var(--border, #dee2e6); border-radius: 4px;
  background: var(--bg-primary, #fff); cursor: pointer;
}
.wz-card:hover { background: var(--bg-secondary, #fafafa); }
.wz-card.on { border-color: var(--accent); background: var(--accent-bg); box-shadow: inset 0 0 0 1px var(--accent); }
.wz-card input { margin: 3px 0 0; flex: 0 0 auto; accent-color: var(--accent); }
.wz-card-txt { display: flex; flex-direction: column; min-width: 0; }
.wz-card-title { font-weight: 600; font-size: 13px; }
.wz-card-desc { color: var(--text-muted, #6b7280); font-size: 12px; margin-top: 2px; }
.wz-card-team { color: var(--text-muted, #6b7280); font-size: 12px; margin-top: 4px; }

/* chips（单选 / 多选同构造） */
.wz-chips { display: flex; flex-wrap: wrap; gap: 8px; }
.wz-chip {
  position: relative; display: inline-flex; align-items: center; gap: 6px;
  padding: 5px 10px; border: 1px solid var(--border, #dee2e6); border-radius: 999px;
  background: var(--bg-primary, #fff); cursor: pointer; font-size: 12px;
}
.wz-chip:hover { background: var(--bg-secondary, #fafafa); }
.wz-chip.on { background: var(--accent); border-color: var(--accent); color: #fff; }
.wz-chip input { position: absolute; opacity: 0; width: 0; height: 0; }

/* 侦察预览 / 只读块 */
.wz-recon {
  padding: 8px 10px; border: 1px solid var(--border, #dee2e6); border-radius: 4px;
  background: var(--bg-secondary, #fafafa); font-size: 12px; color: var(--text-secondary); line-height: 1.6;
}
.wz-placeholder {
  padding: 24px; border: 1px dashed var(--border, #dee2e6); border-radius: 4px;
  color: var(--text-muted, #6b7280); text-align: center; font-size: 13px; line-height: 1.7;
}
.wz-inline-btn { margin-left: 8px; }

/* 快捷（默认按钮） */
.wz-quick { display: flex; align-items: center; gap: 12px; margin-top: 12px; }

/* 团队成员 pills */
.wz-members { display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 8px; }
.wz-member {
  display: inline-flex; align-items: center; gap: 6px;
  border: 1px solid var(--border, #dee2e6); border-radius: 999px; padding: 3px 6px 3px 10px;
  font-size: 12px; background: var(--bg-primary, #fff);
}
.wz-member-tag { font-size: 10px; padding: 1px 5px; border-radius: 6px; background: var(--accent-bg); color: var(--accent); }
.wz-x {
  border: 0; background: none; color: var(--text-muted, #999); cursor: pointer; padding: 0 2px; font-size: 11px;
}
.wz-x:hover { color: var(--danger); }

/* 辅助选项 */
.wz-aux { display: flex; flex-wrap: wrap; gap: 16px; }
.wz-aux-item { display: inline-flex; align-items: center; gap: 8px; font-size: 12px; cursor: pointer; }

/* Step5 预览 */
.wz-pv-toolbar { display: flex; align-items: center; gap: 8px; flex-shrink: 0; }
.wz-scene-name { max-width: 220px; }
.wz-pv-grow { flex: 1; }
.wz-pv-tabs { flex: 1; min-height: 0; }
.wz-pv-split { flex: 1; min-height: 0; display: grid; grid-template-columns: 220px minmax(0, 1fr); gap: 16px; height: 100%; }
.wz-agent-list { display: flex; flex-direction: column; gap: 4px; overflow-y: auto; min-height: 0; }
.wz-group-h { color: var(--text-muted, #6b7280); font-size: 12px; margin-top: 8px; display: flex; align-items: center; }
.wz-agent-item {
  display: flex; align-items: center; gap: 6px; padding: 6px 8px;
  border: 1px solid var(--border, #dee2e6); border-radius: 4px; cursor: pointer; font-size: 12px;
  background: var(--bg-primary, #fff);
}
.wz-agent-item:hover { background: var(--bg-secondary, #fafafa); }
.wz-agent-item.on { border-color: var(--accent); background: var(--accent-bg); }
.wz-agent-item b { font-weight: 600; flex: 0 1 auto; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.wz-agent-item .wz-x { margin-left: auto; }
.wz-src { font-size: 10px; color: var(--text-muted, #6b7280); border: 1px solid var(--border, #dee2e6); border-radius: 3px; padding: 0 4px; white-space: nowrap; margin-left: auto; }
.wz-src.hi { color: var(--success, #2d9f4e); border-color: var(--success, #2d9f4e); }
.wz-src.lo { border-style: dashed; }
.wz-agent-detail { min-width: 0; overflow-y: auto; padding-right: 4px; }
.wz-mem-tabs { height: 100%; }
.wz-empty { color: var(--text-muted, #999); font-size: 12px; padding: 20px; text-align: center; }

/* 生成中 */
.wz-generating {
  position: absolute; right: 24px; bottom: 12px;
  display: flex; align-items: center; gap: 8px; padding: 8px 12px;
  border: 1px solid var(--border, #dee2e6); border-radius: 4px; background: var(--bg-secondary, #fafafa);
  font-size: 12px; box-shadow: 0 4px 12px rgba(0, 0, 0, 0.1);
}
.wz-spinner {
  width: 14px; height: 14px; border: 2px solid var(--accent); border-top-color: transparent;
  border-radius: 50%; animation: wz-spin 0.7s linear infinite;
}
@keyframes wz-spin { to { transform: rotate(360deg); } }

/* 底栏 */
.wz-footer {
  display: flex; align-items: center; gap: 8px;
  padding: 10px 16px; border-top: 1px solid var(--border, #dee2e6); flex-shrink: 0;
}
.wz-footer-grow { flex: 1; }
</style>
