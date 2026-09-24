<template>
  <el-steps :active="activeStep" align-center :process-status="processStatus">
    <el-step
      v-for="(stage, index) in stages"
      :key="stage.name"
      :title="stage.name"
      :status="stepStatus(stage.status)"
    >
      <template #description>
        <div class="stage-desc">
          <div>{{ formatCurrency(stage.amount) }} · {{ stage.dueAt }}</div>
          <el-tag :type="tagType(stage.status)" size="small" effect="light" class="stage-tag">
            第{{ index + 1 }}段 · {{ stageLabel(stage.status) }}
          </el-tag>
          <div v-if="stage.confirmedAt" class="stage-time">付款确认：{{ formatTime(stage.confirmedAt) }}</div>
          <div v-if="stage.submittedAt" class="stage-time">提交时间：{{ formatTime(stage.submittedAt) }}</div>
          <div v-if="stage.status === 'rejected' && stage.rejectReason" class="stage-reject">
            驳回原因：{{ stage.rejectReason }}
          </div>
        </div>
      </template>
    </el-step>
  </el-steps>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { ContractStage } from '../../types';
import { StageStatusLabel } from '../../types/enums';
import { formatCurrency } from '../../utils/formatCurrency';

const props = defineProps<{ stages: ContractStage[] }>();

const stageLabel = (status: string) => StageStatusLabel[status] || status;

// Position of the first unfinished stage; used only to color the connecting line.
const activeStep = computed(() => {
  const idx = props.stages.findIndex((s) => s.status !== 'done');
  return idx === -1 ? props.stages.length : idx;
});

// A submitted stage is "waiting confirmation", rendered as a finished-looking
// step only after it is actually paid; rejected steps stay in error.
const processStatus = computed(() => {
  const current = props.stages[activeStep.value];
  return current && current.status === 'submitted' ? 'finish' : 'process';
});

function stepStatus(status: string): 'wait' | 'process' | 'finish' | 'error' | 'success' {
  switch (status) {
    case 'done':
      return 'success';
    case 'submitted':
      return 'finish';
    case 'rejected':
      return 'error';
    case 'in_progress':
      return 'process';
    default:
      return 'wait';
  }
}

function tagType(status: string): 'success' | 'warning' | 'danger' | 'info' | 'primary' {
  switch (status) {
    case 'done':
      return 'success';
    case 'submitted':
      return 'warning';
    case 'rejected':
      return 'danger';
    case 'in_progress':
      return 'primary';
    default:
      return 'info';
  }
}

function formatTime(value?: string): string {
  if (!value) return '';
  return new Date(value).toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  });
}
</script>

<style scoped>
.stage-desc {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
}
.stage-tag {
  margin: 2px 0;
}
.stage-time {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.stage-reject {
  font-size: 12px;
  color: var(--el-color-danger);
  max-width: 220px;
}
</style>
