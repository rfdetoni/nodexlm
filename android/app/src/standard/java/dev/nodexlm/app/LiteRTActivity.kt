package dev.nodexlm.app

import android.app.Activity
import android.os.Bundle
import android.widget.Toast

/** No SDK classes or native libraries are linked into the universal build. */
class LiteRTActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        Toast.makeText(this, R.string.litert_disabled, Toast.LENGTH_SHORT).show()
        finish()
    }
}
