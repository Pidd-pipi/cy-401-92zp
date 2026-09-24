<template>
  <el-steps :active="activeStep" align-center :finish-status="'success'">
    <el-step
      v-for="(stage, idx) in stages"
      :key="stage.name"
      :title="stage.name"
      :status="stepStatus(idx)"
      :description="`${formatCurrency(stage.amount)} · ${stage.dueAt}`"
    />
  </el-steps>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { ContractStage } from '../../types';
import { formatCurrency } from '../../utils/formatCurrency';

const props = defineProps<{ stages: ContractStage[] }>();

// el-steps active index: the first stage that is not paid yet.
const activeStep = computed(() => {
  const idx = props.stages.findIndex((s) => s.status !== 'paid' && s.status !== 'done');
  return idx === -1 ? props.stages.length : idx;
});

function stepStatus(idx: number): 'wait' | 'process' | 'finish' | 'error' | 'success' {
  const s = props.stages[idx]?.status;
  if (s === 'paid' || s === 'done') return 'success';
  if (s === 'rejected') return 'error';
  if (s === 'submitted' || s === 'in_progress') return 'process';
  return 'wait';
}
</script>
