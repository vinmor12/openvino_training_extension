/**
 * Copyright (c) 2020 Intel Corporation
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 * http://www.apache.org/licenses/LICENSE-2.0
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import {Component, Inject, OnDestroy, OnInit} from '@angular/core';
import {FormBuilder, FormGroup, Validators} from '@angular/forms';
import {MAT_DIALOG_DATA} from '@angular/material/dialog';
import {IAbstractList} from '@idlp/root/models';
import {Observable, of, Subject} from 'rxjs';
import {filter, startWith, takeUntil} from 'rxjs/operators';
import {WS} from '@idlp/root/ws.events';
import {WebsocketService} from '@idlp/providers/websocket.service';
import {ActivatedRoute} from '@angular/router';
import {IBuild, IModel} from '@idlp/routed/problem-info/problem-info.models';

@Component({
  selector: 'idlp-fine-tune-dialog',
  templateUrl: './fine-tune-dialog.component.html',
  styleUrls: ['./fine-tune-dialog.component.scss']
})
export class IdlpFineTuneDialogComponent implements OnInit, OnDestroy {
  builds: IBuild[] = [];
  filteredBuilds: Observable<IBuild[]> = of([]);

  buildId: string;
  model: IModel;

  form: FormGroup;

  private destroy$: Subject<any> = new Subject();

  constructor(
    private fb: FormBuilder,
    private route: ActivatedRoute,
    private websocketService: WebsocketService,
    @Inject(MAT_DIALOG_DATA) public data: any
  ) {
    this.buildId = data.buildId;
    this.model = data.model;

    this.websocketService
      .on<IAbstractList<IBuild>>(WS.ON.BUILD_LIST)
      .pipe(
        takeUntil(this.destroy$),
        filter((builds: IAbstractList<IBuild>) => builds?.items?.length > 0)
      )
      .subscribe((builds: IAbstractList<IBuild>) => {
        this.builds = builds.items.filter((build: IBuild) => build.status !== 'tmp' && build.name !== 'default');
        this.builds = this.builds.sort((buildA: IBuild, buildB: IBuild) => buildA.name?.trim().toLocaleLowerCase() > buildB.name?.trim().toLocaleLowerCase() ? -1 : 1);
        this.filteredBuilds = of(this.builds);
      });

    this.form = this.fb.group({
      batchSize: 1,
      gpuNumber: 1,
      saveAnnotatedValImages: [false, Validators.required],
      advanced: [false, Validators.required],
      name: [this.getDefaultModelName(data.problemName), Validators.required],
      build: [null, Validators.required],
      epochs: [5, [Validators.required, Validators.min(1), Validators.max(100)]]
    });
  }

  get formValid(): boolean {
    return this.form.valid && (this.form.get('build').value instanceof Object);
  }

  get advancedEnabled(): boolean {
    return this.form.get('advanced').value;
  }

  ngOnInit(): void {
    this.websocketService.send(WS.SEND.BUILD_LIST, {problemId: this.data.problemId});

    this.form.get('advanced').valueChanges
      .pipe(
        takeUntil(this.destroy$)
      )
      .subscribe((enable: any) => {
        if (enable) {
          this.form.get('batchSize').setValidators([Validators.required, Validators.min(1), Validators.max(1000)]);
          this.form.get('gpuNumber').setValidators([Validators.required, Validators.min(1), Validators.max(100)]);
        } else {
          this.form.get('batchSize').clearValidators();
          this.form.get('gpuNumber').clearValidators();
        }
        this.form.get('batchSize').updateValueAndValidity();
        this.form.get('gpuNumber').updateValueAndValidity();
      });

    this.form.get('build').valueChanges
      .pipe(
        startWith(''),
        takeUntil(this.destroy$)
      )
      .subscribe((filterValue: any) => {
        if (typeof filterValue !== 'string') {
          return;
        }
        this.filteredBuilds = of(this.builds?.filter((build: IBuild) =>
          build.name?.trim().toLowerCase().includes(filterValue.trim().toLowerCase())
        ));
      });
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }

  buildDisplayFn(build?: IBuild): string {
    return build?.name ?? '';
  }

  getMetricAlign(metricIndex: number): string {
    if (metricIndex > 2) {
      return this.getMetricAlign(metricIndex - 3);
    }
    return metricIndex === 0 ? 'flex-start' : metricIndex === 1 ? 'center' : 'flex-end';
  }

  private getDefaultModelName(problemName = ''): string {
    return `${problemName.toLocaleLowerCase().split(' ').join('-')}-${Date.now()}`;
  }
}
