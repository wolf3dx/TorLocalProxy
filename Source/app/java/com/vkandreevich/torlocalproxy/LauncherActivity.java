package com.vkandreevich.torlocalproxy;

import android.app.Activity;
import android.content.Intent;
import android.os.Build;
import android.os.Bundle;

/**
 * Стартовая точка приложения: поднимает службу переднего плана и сразу
 * передаёт управление окну, которое рисует Fyne.
 *
 * Отдельная activity нужна потому, что штатную activity Fyne менять
 * нельзя — она приходит из его библиотеки, а службу кто-то должен
 * запустить. Своего вида у этой activity нет, она закрывается сразу.
 *
 * Окно вызывается по имени класса, а не по самому классу: так у нас нет
 * зависимости сборки от кода Fyne.
 */
public class LauncherActivity extends Activity {

    @Override
    protected void onCreate(Bundle state) {
        super.onCreate(state);

        Intent service = new Intent(this, TorService.class);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            startForegroundService(service);
        } else {
            startService(service);
        }

        Intent window = new Intent();
        window.setClassName(this, "org.golang.app.GoNativeActivity");
        startActivity(window);

        finish();
    }
}
